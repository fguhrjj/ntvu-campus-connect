// Package app schedules connection attempts (spec §5): one at a time,
// coalesced triggers, backoff and pause.
package app

import (
	"context"
	"sync"
	"time"

	"campusconnect/internal/flow"
)

// Trigger names why a connection attempt runs.
type Trigger string

const (
	TriggerStartup  Trigger = "startup"
	TriggerWLAN     Trigger = "WLAN"
	TriggerResume   Trigger = "resume"
	TriggerTimer    Trigger = "timer"
	TriggerManual   Trigger = "manual"
	TriggerSettings Trigger = "settings"
)

// State is broadcast after every attempt.
type State struct {
	Status    flow.Status
	Message   string
	CheckedAt time.Time
	Trigger   Trigger
	// Paused is the status that paused automatic login, or "".
	Paused flow.Status
	// NextFull is when backoff ends (zero if not backing off).
	NextFull time.Time
}

// Runner owns the schedule. Set the fields, then call Run once.
type Runner struct {
	// Connect performs one attempt; build fresh HTTP state inside per call.
	Connect func(context.Context, flow.Mode) flow.Result
	// OnState receives every state change; called from the Run goroutine.
	OnState func(State)
	// Logf receives scheduling decisions (optional).
	Logf func(format string, args ...any)

	// SubscribeWLAN / SubscribeResume are optional event sources.
	SubscribeWLAN   func(context.Context, func(ssid string)) (func(), error)
	SubscribeResume func(context.Context, func()) (func(), error)
	TargetSSID      string

	// Interval is the patrol period (spec: 60 s); SettleDelay waits for DHCP
	// after WLAN/resume (spec: 3 s).
	Interval    time.Duration
	SettleDelay time.Duration

	initOnce sync.Once
	wake     chan struct{}
	mu       sync.Mutex
	pending  map[Trigger]bool
	clear    bool
	state    State
}

func (r *Runner) init() {
	r.initOnce.Do(func() {
		r.wake = make(chan struct{}, 1)
		r.pending = map[Trigger]bool{}
	})
}

// Trigger queues an attempt. Triggers arriving while one runs coalesce
// into a single follow-up attempt. Safe from any goroutine.
func (r *Runner) Trigger(t Trigger) {
	r.init()
	r.mu.Lock()
	r.pending[t] = true
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// SettingsSaved clears any pause and runs immediately.
func (r *Runner) SettingsSaved() {
	r.init()
	r.mu.Lock()
	r.clear = true
	r.mu.Unlock()
	r.Trigger(TriggerSettings)
}

// State returns the latest broadcast state.
func (r *Runner) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// Run blocks until ctx ends.
func (r *Runner) Run(ctx context.Context) error {
	r.init()
	r.subscribe(ctx)
	r.Trigger(TriggerStartup)
	ticker := time.NewTicker(or(r.Interval, time.Minute))
	defer ticker.Stop()
	// retry fires when backoff ends, so full retries keep the spec §5
	// spacing instead of waiting for the next patrol tick.
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()
	var s schedule
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.Trigger(TriggerTimer)
		case <-retry.C:
			r.Trigger(TriggerTimer)
		case <-r.wake:
			r.mu.Lock()
			triggers, clear := r.pending, r.clear
			r.pending, r.clear = map[Trigger]bool{}, false
			r.mu.Unlock()
			if len(triggers) == 0 {
				continue
			}
			if clear {
				s.clearPause()
			}
			mode := r.attempt(ctx, &s, pick(triggers))
			retry.Stop()
			if d, ok := s.retryAfter(mode == flow.ModeFull, time.Now()); ok {
				retry.Reset(d)
			}
		}
	}
}

// pick chooses the trigger that grants the most (manual/settings → full).
func pick(ts map[Trigger]bool) Trigger {
	for _, t := range []Trigger{TriggerSettings, TriggerManual, TriggerStartup, TriggerWLAN, TriggerResume, TriggerTimer} {
		if ts[t] {
			return t
		}
	}
	return TriggerTimer
}

// attempt runs one connection attempt and returns the mode it used.
func (r *Runner) attempt(ctx context.Context, s *schedule, t Trigger) flow.Mode {
	mode := s.mode(t, time.Now())
	r.logf("触发：%s，模式：%s", t, modeName(mode))
	res := r.Connect(ctx, mode)
	if ctx.Err() != nil {
		return mode
	}
	now := time.Now()
	s.record(mode, res, now)
	r.mu.Lock()
	st := State{Status: res.Status, Message: res.Message, CheckedAt: now, Trigger: t, Paused: s.pause.Status, NextFull: s.nextFull}
	if res.Status == flow.Deferred {
		// Check-only run found the portal offline: keep showing why automatic
		// login is held back (pause, else backoff, else the last state).
		held, ok := s.held()
		if !ok {
			held = flow.Result{Status: r.state.Status, Message: r.state.Message}
		}
		st.Status, st.Message = held.Status, held.Message
	}
	r.state = st
	r.mu.Unlock()
	r.logf("结果：%s %s", res.Status, res.Message)
	if r.OnState != nil {
		r.OnState(st)
	}
	return mode
}

func (r *Runner) subscribe(ctx context.Context) {
	delayed := func(t Trigger) {
		go func() {
			timer := time.NewTimer(or(r.SettleDelay, 3*time.Second))
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
				r.Trigger(t)
			}
		}()
	}
	if r.SubscribeWLAN != nil {
		if _, err := r.SubscribeWLAN(ctx, func(ssid string) {
			if ssid == "" || ssid == r.TargetSSID {
				delayed(TriggerWLAN)
			}
		}); err != nil {
			r.logf("订阅 WLAN 通知失败：%v", err)
		}
	}
	if r.SubscribeResume != nil {
		if _, err := r.SubscribeResume(ctx, func() { delayed(TriggerResume) }); err != nil {
			r.logf("订阅唤醒通知失败：%v", err)
		}
	}
}

func (r *Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

func modeName(m flow.Mode) string {
	if m == flow.ModeCheckOnly {
		return "仅检查"
	}
	return "完整"
}

func or(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}
