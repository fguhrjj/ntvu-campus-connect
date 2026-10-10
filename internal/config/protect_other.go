//go:build !windows

package config

type unsupportedProtector struct{}

// NewProtector returns a protector that always fails off Windows.
func NewProtector() Protector { return unsupportedProtector{} }

func (unsupportedProtector) Protect([]byte) (string, error)   { return "", ErrUnsupported }
func (unsupportedProtector) Unprotect(string) ([]byte, error) { return nil, ErrUnsupported }
