//go:build !windows

package autostart

// Enable is unavailable off Windows.
func Enable(string, string) error { return ErrUnsupported }

// Disable is unavailable off Windows.
func Disable(string) error { return ErrUnsupported }
