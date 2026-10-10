//go:build !windows

package app

import "errors"

// AcquireSingleInstance is unavailable off Windows.
func AcquireSingleInstance() (release func(), acquired bool, err error) {
	return nil, false, errors.New("app: single instance requires Windows")
}
