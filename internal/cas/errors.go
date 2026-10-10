package cas

import "fmt"

// Kind classifies protocol-level authentication outcomes.
type Kind string

const (
	AuthenticationRejected Kind = "AuthenticationRejected"
	AccountLocked          Kind = "AccountLocked"
	AccountRisk            Kind = "AccountRisk"
	CaptchaRequired        Kind = "CaptchaRequired"
	NeedsAttention         Kind = "NeedsAttention"
	PortalChanged          Kind = "PortalChanged"
	PortalUnavailable      Kind = "PortalUnavailable"
)

// Error is a classified CAS failure. Detail is server text with no password or
// ticket values; URLs are never stored here.
type Error struct {
	Kind   Kind
	Step   string
	Detail string
	Err    error
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("cas %s: %s", e.Step, e.Kind)
	}
	return fmt.Sprintf("cas %s: %s: %s", e.Step, e.Kind, e.Detail)
}

func (e *Error) Unwrap() error { return e.Err }

func classified(step string, kind Kind, detail string, err error) error {
	return &Error{Kind: kind, Step: step, Detail: detail, Err: err}
}
