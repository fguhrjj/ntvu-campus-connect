//go:build windows

package autostart

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// Enable sets the Run value name to command, normally the quoted path of
// the executable.
func Enable(name, command string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("autostart: open Run key: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(name, command); err != nil {
		return fmt.Errorf("autostart: set %s: %w", name, err)
	}
	return nil
}

// Disable removes the Run value name. A missing value is not an error.
func Disable(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("autostart: open Run key: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("autostart: delete %s: %w", name, err)
	}
	return nil
}
