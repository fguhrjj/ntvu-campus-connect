// Package portal talks to the Ruijie ePortal at nap.ntvu.edu.cn (spec §2.1–2.3).
package portal

import "fmt"

// FlowKey is the workflow key the portal frontend uses for login.
const FlowKey = "portal_auth"

// Session is what Discover learns from the portal-main redirect.
type Session struct {
	ID, UserIP, UserMAC, SSID, NASIP, CustomPageID string
	// AdvertisedOnline is the redirect's userOnline flag; informational only.
	AdvertisedOnline bool
}

// Node is the portal workflow position (getCurrentNode).
type Node struct {
	Path string
}

// Service is one operator choice from serviceSelection.
type Service struct {
	Label, Value string
}

// OnlineInfo is the subset of getOnlineUserInfo the program uses.
type OnlineInfo struct {
	UserID, Service string
}

// Kind classifies portal failures for the connection state machine.
type Kind int

const (
	// KindUnavailable: transport failure, 5xx, or an API code other than 200.
	KindUnavailable Kind = iota + 1
	// KindChanged: response shape or redirect does not match the known protocol.
	KindChanged
	// KindRejected: serviceLogin refused by the portal.
	KindRejected
)

func (k Kind) String() string {
	switch k {
	case KindUnavailable:
		return "unavailable"
	case KindChanged:
		return "changed"
	case KindRejected:
		return "rejected"
	}
	return "unknown"
}

// Error is returned by every Client method.
type Error struct {
	Kind Kind
	Op   string
	// Code is the API envelope code when the portal answered with code != 200.
	Code    int
	Message string
	Err     error
}

func (e *Error) Error() string {
	s := fmt.Sprintf("portal %s: %s", e.Op, e.Kind)
	if e.Code != 0 {
		s += fmt.Sprintf(": code %d", e.Code)
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

func (e *Error) Unwrap() error { return e.Err }
