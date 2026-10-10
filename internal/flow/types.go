// Package flow runs one bounded connection attempt (spec §4) and classifies
// its outcome into the statuses of spec §6.
package flow

import (
	"context"

	"campusconnect/internal/cas"
	"campusconnect/internal/portal"
)

// Status is a spec §6 result state.
type Status string

const (
	Connected                    Status = "Connected"
	AlreadyOnline                Status = "AlreadyOnline"
	OnlineButInternetUnavailable Status = "OnlineButInternetUnavailable"
	NotTargetNetwork             Status = "NotTargetNetwork"
	NetworkNotReady              Status = "NetworkNotReady"
	PortalUnavailable            Status = "PortalUnavailable"
	TimedOut                     Status = "TimedOut"
	ConfigurationRequired        Status = "ConfigurationRequired"
	AuthenticationRejected       Status = "AuthenticationRejected"
	AccountLocked                Status = "AccountLocked"
	AccountRisk                  Status = "AccountRisk"
	CaptchaRequired              Status = "CaptchaRequired"
	ServiceNotFound              Status = "ServiceNotFound"
	NeedsAttention               Status = "NeedsAttention"
	PortalChanged                Status = "PortalChanged"
	InternalError                Status = "InternalError"
	// Deferred is internal: a check-only run found the portal offline and
	// stopped before any write. The runner keeps showing the previous result.
	Deferred Status = "Deferred"
)

// Online reports whether the status means the portal session is online.
func (s Status) Online() bool {
	return s == Connected || s == AlreadyOnline || s == OnlineButInternetUnavailable
}

// Result is the outcome of one Connect. Message never contains URLs,
// session ids, tickets or credentials.
type Result struct {
	Status  Status
	Message string
}

// Mode selects whether Connect may submit anything.
type Mode int

const (
	// ModeFull may authenticate and select a service.
	ModeFull Mode = iota
	// ModeCheckOnly only discovers and checks the online state.
	ModeCheckOnly
)

// Portal is the subset of portal.Client used by the flow.
type Portal interface {
	Discover(context.Context) (portal.Session, error)
	UserOnline(context.Context, string) (bool, error)
	OnlineUserInfo(context.Context, string) (portal.OnlineInfo, bool, error)
	CurrentNode(context.Context, string, string) (portal.Node, error)
	Services(context.Context, string) ([]portal.Service, error)
	ServiceLogin(context.Context, string, string) error
}

// Authenticator is cas.Authenticator.
type Authenticator interface {
	Login(context.Context, cas.PortalContext, cas.Credentials) (cas.Result, error)
}

// Probe checks real internet reachability (portal.InternetProbe).
type Probe interface {
	Internet(context.Context) bool
}
