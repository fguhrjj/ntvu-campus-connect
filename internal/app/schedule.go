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
	paused   flow.Status // non-empty: automatic login paused until settings change
}

// mode returns ModeFull when this trigger may submit credentials/service.
func (s *schedule) mode(t Trigger, now time.Time) flow.Mode {
	if t == TriggerManual || t == TriggerSettings {
		return flow.ModeFull
	}
	if s.paused != "" || now.Before(s.nextFull) {
		return flow.ModeCheckOnly
	}
	return flow.ModeFull
}

// record updates state and returns when backoff ends (zero if not backing off).
func (s *schedule) record(m flow.Mode, r flow.Result, now time.Time) time.Time {
	switch {
	case r.Status.Online():
		s.failures, s.nextFull = 0, time.Time{}
	case pauses(r.Status):
		s.paused = r.Status
		s.failures, s.nextFull = 0, time.Time{}
	case m == flow.ModeFull && backsOff(r.Status):
		s.nextFull = now.Add(backoff[min(s.failures, len(backoff)-1)])
		s.failures++
		return s.nextFull
	}
	return time.Time{}
}

func (s *schedule) clearPause() {
	s.paused = ""
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
