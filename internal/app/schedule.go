package app

import (
	"time"

	"campusconnect/internal/flow"
)

// backoff is the spec §5 sequence; the last step repeats.
var backoff = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute}

// schedule decides whether a trigger may log in and updates backoff/pause
// state from results. It is not safe for concurrent use; Runner serialises it.
type schedule struct {
	failures int
	nextFull time.Time   // zero: no backoff
	pause    flow.Result // Status non-empty: automatic login paused until settings change
	failure  flow.Result // the full attempt that started or extended the backoff
}

// mode returns ModeFull when this trigger may submit credentials/service.
func (s *schedule) mode(t Trigger, now time.Time) flow.Mode {
	if t == TriggerManual || t == TriggerSettings {
		return flow.ModeFull
	}
	if s.pause.Status != "" || now.Before(s.nextFull) {
		return flow.ModeCheckOnly
	}
	return flow.ModeFull
}

// record updates state and returns when backoff ends (zero if not backing off).
func (s *schedule) record(m flow.Mode, r flow.Result, now time.Time) time.Time {
	switch {
	case r.Status.Online():
		s.failures, s.nextFull, s.failure = 0, time.Time{}, flow.Result{}
	case pauses(r.Status):
		s.pause = r
		s.failures, s.nextFull = 0, time.Time{}
	case m == flow.ModeFull && backsOff(r.Status):
		s.failure = r
		s.nextFull = now.Add(backoff[min(s.failures, len(backoff)-1)])
		s.failures++
		return s.nextFull
	}
	return time.Time{}
}

// held is what a Deferred check-only run keeps showing: the result that
// paused automatic login, else the one that started the backoff.
func (s *schedule) held() (flow.Result, bool) {
	if s.pause.Status != "" {
		return s.pause, true
	}
	if s.failure.Status != "" {
		return s.failure, true
	}
	return flow.Result{}, false
}

// retryAfter says when the runner should raise an automatic trigger so the
// next full attempt runs as soon as backoff ends (spec §5: 1, 2, 5 minutes
// after each failure, not at the next patrol tick). ok is false when no
// such retry is due: no backoff, paused, or a full attempt already ran
// after the backoff ended.
func (s *schedule) retryAfter(ranFull bool, now time.Time) (d time.Duration, ok bool) {
	if s.nextFull.IsZero() || s.pause.Status != "" {
		return 0, false
	}
	d = s.nextFull.Sub(now)
	if d <= 0 && ranFull {
		return 0, false
	}
	return max(d, 0), true
}

func (s *schedule) clearPause() {
	s.pause, s.failure = flow.Result{}, flow.Result{}
	s.failures, s.nextFull = 0, time.Time{}
}

// pauses lists the spec §5 statuses that stop automatic login, plus
// NeedsAttention (ledger ruling: unknown CAS outcomes may be credential problems).
func pauses(s flow.Status) bool {
	switch s {
	case flow.AuthenticationRejected, flow.AccountLocked, flow.AccountRisk, flow.CaptchaRequired,
		flow.ServiceNotFound, flow.ConfigurationRequired, flow.NeedsAttention:
		return true
	}
	return false
}

func backsOff(s flow.Status) bool {
	switch s {
	case flow.NetworkNotReady, flow.PortalUnavailable, flow.TimedOut, flow.PortalChanged, flow.InternalError:
		return true
	}
	return false
}
