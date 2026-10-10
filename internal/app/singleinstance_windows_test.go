//go:build windows

package app

import (
	"fmt"
	"os"
	"testing"
)

func TestSingleInstanceRejectsSecondAcquire(t *testing.T) {
	name := fmt.Sprintf(`Local\CampusConnectTest-%d`, os.Getpid())
	release, ok, err := acquireNamed(name)
	if err != nil || !ok {
		t.Fatalf("first acquire = %v, %v", ok, err)
	}
	_, ok2, err := acquireNamed(name)
	if err != nil || ok2 {
		t.Fatalf("second acquire = %v, %v; want rejected", ok2, err)
	}
	release()
	release3, ok3, err := acquireNamed(name)
	if err != nil || !ok3 {
		t.Fatalf("acquire after release = %v, %v", ok3, err)
	}
	release3()
}
