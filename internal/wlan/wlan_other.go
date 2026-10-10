//go:build !windows

package wlan

import "context"

// CurrentSSID is unavailable off Windows.
func CurrentSSID() (string, error) { return "", ErrUnsupported }

// Subscribe is unavailable off Windows.
func Subscribe(context.Context, func(string)) (func(), error) { return nil, ErrUnsupported }
