// Package autostart registers the program under the current user's Run key
// (spec §3: HKCU\Software\Microsoft\Windows\CurrentVersion\Run).
package autostart

import "errors"

// ErrUnsupported is returned off Windows.
var ErrUnsupported = errors.New("autostart: requires Windows")
