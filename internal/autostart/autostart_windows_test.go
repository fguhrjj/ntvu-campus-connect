//go:build windows

package autostart

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// testValue is never the production value name "CampusConnect".
const testValue = "CampusConnectTest"

func readRunValue(t *testing.T) (string, error) {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue(testValue)
	return v, err
}

func TestEnableWritesRunValueAndDisableRemovesIt(t *testing.T) {
	t.Cleanup(func() { Disable(testValue) })
	command := `"C:\Program Files\CampusConnect Test\CampusConnect.exe"`

	if err := Enable(testValue, command); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if got, err := readRunValue(t); err != nil || got != command {
		t.Fatalf("Run value = %q, %v; want %q", got, err, command)
	}
	if err := Disable(testValue); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if _, err := readRunValue(t); !errors.Is(err, registry.ErrNotExist) {
		t.Fatalf("Run value after Disable: err = %v, want ErrNotExist", err)
	}
	if err := Disable(testValue); err != nil {
		t.Fatalf("Disable of a missing value: %v", err)
	}
}
