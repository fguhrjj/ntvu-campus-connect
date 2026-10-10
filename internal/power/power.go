// Package power reports system resume (spec §5) through
// PowerRegisterSuspendResumeNotification callbacks, without a window.
package power

import (
	"context"
	"errors"

	"campusconnect/internal/notify"
)

// ErrUnsupported is returned off Windows.
var ErrUnsupported = errors.New("power: resume notifications require Windows")

// PBT_* event types passed to the callback.
const (
	pbtAPMSuspend         = 0x4
	pbtAPMResumeSuspend   = 0x7
	pbtAPMResumeAutomatic = 0x12
)

var registry notify.Registry[struct{}]

func subscribe(ctx context.Context, onResume func(), register func(uintptr) (func(), error)) (func(), error) {
	return registry.Subscribe(ctx, func(struct{}) { onResume() }, register)
}

// handleEvent is the body of the OS callback.
func handleEvent(id uintptr, eventType uint32) {
	if eventType == pbtAPMResumeAutomatic || eventType == pbtAPMResumeSuspend {
		registry.Deliver(id, struct{}{})
	}
}
