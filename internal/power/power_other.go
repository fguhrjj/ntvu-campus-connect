//go:build !windows

package power

import "context"

// SubscribeResume is unavailable off Windows.
func SubscribeResume(context.Context, func()) (func(), error) {
	return nil, ErrUnsupported
}
