//go:build windows

package app

import (
	"errors"

	"golang.org/x/sys/windows"
)

// AcquireSingleInstance takes the Local\CampusConnect mutex. acquired is
// false when another instance in this session already holds it.
func AcquireSingleInstance() (release func(), acquired bool, err error) {
	return acquireNamed(`Local\CampusConnect`)
}

func acquireNamed(name string) (func(), bool, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateMutex(nil, false, p)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(h)
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return func() { windows.CloseHandle(h) }, true, nil
}
