package app

import (
	"context"
	"testing"
	"time"

	"campusconnect/internal/flow"
)

// harness runs a Runner whose Connect reports each call and returns the
// next scripted result (the last one repeats).
type harness struct {
	r       *Runner
	calls   chan flow.Mode
	release chan struct{} // when non-nil, Connect blocks until it can receive
	results []flow.Status
	states  chan State
	cancel  context.CancelFunc
}

func newHarness(t *testing.T, results ...flow.Status) *harness {
	t.Helper()
	h := &harness{calls: make(chan flow.Mode, 16), results: results, states: make(chan State, 64)}
	n := 0
	h.r = &Runner{
		Connect: func(ctx context.Context, m flow.Mode) flow.Result {
			h.calls <- m
			if h.release != nil {
				<-h.release
			}
			s := h.results[min(n, len(h.results)-1)]
			n++
			return flow.Result{Status: s}
		},
		OnState:     func(s State) { h.states <- s },
		Interval:    time.Hour,
		SettleDelay: time.Nanosecond,
	}
	return h
}

func (h *harness) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	done := make(chan struct{})
	go func() { h.r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	if m := h.next(t); m != flow.ModeFull {
		t.Fatalf("startup mode = %v, want full", m)
	}
}

func (h *harness) next(t *testing.T) flow.Mode {
	t.Helper()
	select {
	case m := <-h.calls:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("Connect was not called")
	}
	return 0
}

func (h *harness) none(t *testing.T) {
	t.Helper()
	select {
	case m := <-h.calls:
		t.Fatalf("unexpected Connect(%v)", m)
	case <-time.After(50 * time.Millisecond):
	}
}

func (h *harness) lastState(t *testing.T) State {
	t.Helper()
	var s State
	for {
		select {
		case s = <-h.states:
		case <-time.After(50 * time.Millisecond):
			return s
		}
	}
}

func TestConcurrentTriggersCoalesce(t *testing.T) {
	h := newHarness(t, flow.AlreadyOnline)
	h.release = make(chan struct{})
	go func() { h.release <- struct{}{} }() // let startup finish
	h.start(t)

	h.r.Trigger(TriggerManual)
	h.next(t) // blocked inside Connect now
	for range 5 {
		h.r.Trigger(TriggerTimer)
	}
	h.r.Trigger(TriggerManual)
	h.release <- struct{}{}
	if m := h.next(t); m != flow.ModeFull {
		t.Fatalf("coalesced run mode = %v, want full (manual wins)", m)
	}
	h.release <- struct{}{}
	h.none(t)
}

func TestBackoffSequenceIsOneTwoFiveMinutes(t *testing.T) {
	var s schedule
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	for i, want := range []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		if m := s.mode(TriggerTimer, now); m != flow.ModeFull {
			t.Fatalf("attempt %d mode = %v, want full", i, m)
		}
		retry := s.record(flow.ModeFull, flow.Result{Status: flow.PortalUnavailable}, now)
		if got := retry.Sub(now); got != want {
			t.Fatalf("backoff %d = %v, want %v", i, got, want)
		}
		if m := s.mode(TriggerTimer, now.Add(want-time.Second)); m != flow.ModeCheckOnly {
			t.Fatalf("during backoff %d mode = %v, want check-only", i, m)
		}
		// Check-only failures during backoff do not push it further out.
		if r := s.record(flow.ModeCheckOnly, flow.Result{Status: flow.PortalUnavailable}, now.Add(time.Second)); !r.IsZero() {
			t.Fatalf("check-only failure rescheduled backoff to %v", r)
		}
		now = now.Add(want)
	}
	// Being online resets the sequence.
	s.record(flow.ModeCheckOnly, flow.Result{Status: flow.AlreadyOnline}, now)
	retry := s.record(flow.ModeFull, flow.Result{Status: flow.TimedOut}, now)
	if got := retry.Sub(now); got != time.Minute {
		t.Fatalf("backoff after reset = %v, want 1m", got)
	}
}

func TestBackoffMakesAutomaticRunsCheckOnly(t *testing.T) {
	h := newHarness(t, flow.PortalUnavailable, flow.Deferred)
	h.start(t)
	for _, tr := range []Trigger{TriggerTimer, TriggerWLAN, TriggerResume} {
		h.r.Trigger(tr)
		if m := h.next(t); m != flow.ModeCheckOnly {
			t.Fatalf("%s during backoff: mode = %v, want check-only", tr, m)
		}
	}
	st := h.lastState(t)
	if st.Status != flow.PortalUnavailable || st.NextFull.IsZero() {
		t.Fatalf("state = %+v, want PortalUnavailable kept with retry time", st)
	}
}

func TestDeferredDuringBackoffShowsBackoffReason(t *testing.T) {
	// Leaving and rejoining NTVU-R during backoff: the Deferred run must
	// show why login is held back, not the intermediate NotTargetNetwork.
	h := newHarness(t, flow.PortalUnavailable, flow.NotTargetNetwork, flow.Deferred)
	h.start(t)
	for range 2 {
		h.r.Trigger(TriggerTimer)
		if m := h.next(t); m != flow.ModeCheckOnly {
			t.Fatalf("during backoff: mode = %v, want check-only", m)
		}
	}
	if st := h.lastState(t); st.Status != flow.PortalUnavailable || st.NextFull.IsZero() {
		t.Fatalf("state = %+v, want the backoff reason PortalUnavailable", st)
	}
}

func TestBackoffEndTriggersFullRetryWithoutTicker(t *testing.T) {
	saved := backoff
	backoff = []time.Duration{20 * time.Millisecond, 40 * time.Millisecond}
	t.Cleanup(func() { backoff = saved })
	h := newHarness(t, flow.PortalUnavailable, flow.PortalUnavailable, flow.NotTargetNetwork)
	h.start(t) // Interval is an hour: only the backoff timer can retry
	for i := range 2 {
		if m := h.next(t); m != flow.ModeFull {
			t.Fatalf("retry %d mode = %v, want full", i, m)
		}
	}
	// The last full attempt neither failed with backoff nor paused: no
	// further retries until a real trigger.
	h.none(t)
}

func TestAuthenticationFailurePausesAutomaticTriggers(t *testing.T) {
	h := newHarness(t, flow.AuthenticationRejected, flow.Deferred, flow.Deferred, flow.Deferred, flow.AlreadyOnline, flow.Deferred)
	h.start(t)
	for _, tr := range []Trigger{TriggerTimer, TriggerWLAN, TriggerResume, TriggerTimer, TriggerTimer} {
		h.r.Trigger(tr)
		if m := h.next(t); m != flow.ModeCheckOnly {
			t.Fatalf("%s while paused: mode = %v, want check-only", tr, m)
		}
	}
	st := h.lastState(t)
	if st.Paused != flow.AuthenticationRejected {
		t.Fatalf("paused = %q; being online via the browser must not clear the pause", st.Paused)
	}
	if st.Status != flow.AuthenticationRejected {
		t.Fatalf("status = %s; after the browser session drops, Deferred must show the pause reason", st.Status)
	}
}

func TestManualTriggerBypassesPause(t *testing.T) {
	h := newHarness(t, flow.CaptchaRequired)
	h.start(t)
	h.r.Trigger(TriggerManual)
	if m := h.next(t); m != flow.ModeFull {
		t.Fatalf("manual while paused: mode = %v, want full", m)
	}
	h.r.Trigger(TriggerTimer)
	if m := h.next(t); m != flow.ModeCheckOnly {
		t.Fatalf("pause must survive a failed manual attempt; mode = %v", m)
	}
}

func TestSettingsSavedClearsPauseAndRunsNow(t *testing.T) {
	h := newHarness(t, flow.ConfigurationRequired, flow.Connected)
	h.start(t)
	h.r.SettingsSaved()
	if m := h.next(t); m != flow.ModeFull {
		t.Fatalf("after save: mode = %v, want full", m)
	}
	h.r.Trigger(TriggerTimer)
	if m := h.next(t); m != flow.ModeFull {
		t.Fatalf("timer after save: mode = %v, want full", m)
	}
	if st := h.lastState(t); st.Paused != "" {
		t.Fatalf("still paused: %+v", st)
	}
}

func TestWLANAndResumeWaitForSettleDelay(t *testing.T) {
	h := newHarness(t, flow.AlreadyOnline)
	h.r.SettleDelay = 150 * time.Millisecond
	var resume func()
	h.r.SubscribeResume = func(_ context.Context, fn func()) (func(), error) {
		resume = fn
		return func() {}, nil
	}
	h.start(t)
	start := time.Now()
	resume()
	h.next(t)
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("resume ran after %v, want ≥ settle delay", d)
	}
}

func TestWLANNotificationTriggersOnlyForTargetSSID(t *testing.T) {
	h := newHarness(t, flow.AlreadyOnline)
	var notify func(string)
	h.r.TargetSSID = "NTVU-R"
	h.r.SubscribeWLAN = func(_ context.Context, fn func(string)) (func(), error) {
		notify = fn
		return func() {}, nil
	}
	h.start(t)
	notify("Home-WiFi")
	h.none(t)
	notify("NTVU-R")
	h.next(t)
	notify("") // SSID withheld: still try (spec §4)
	h.next(t)
}

func TestTickerPatrols(t *testing.T) {
	h := newHarness(t, flow.AlreadyOnline)
	h.r.Interval = 30 * time.Millisecond
	h.start(t)
	h.next(t)
	h.next(t)
}
