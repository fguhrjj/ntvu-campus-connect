//go:build windows

package power

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const deviceNotifyCallback = 2 // DEVICE_NOTIFY_CALLBACK

var (
	powrprof       = windows.NewLazySystemDLL("powrprof.dll")
	procRegister   = powrprof.NewProc("PowerRegisterSuspendResumeNotification")
	procUnregister = powrprof.NewProc("PowerUnregisterSuspendResumeNotification")

	// One stdcall trampoline for the process; Windows passes our id as Context.
	callback = windows.NewCallback(func(context uintptr, eventType uint32, _ uintptr) uintptr {
		handleEvent(context, eventType)
		return 0 // ERROR_SUCCESS
	})
)

// deviceNotifySubscribeParameters is DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS.
type deviceNotifySubscribeParameters struct {
	Callback uintptr
	Context  uintptr
}

// SubscribeResume calls onResume after the system wakes. onResume runs on an
// OS thread and must not block.
func SubscribeResume(ctx context.Context, onResume func()) (func(), error) {
	return subscribe(ctx, onResume, func(id uintptr) (func(), error) {
		params := &deviceNotifySubscribeParameters{Callback: callback, Context: id}
		var handle uintptr
		r, _, _ := procRegister.Call(deviceNotifyCallback, uintptr(unsafe.Pointer(params)), uintptr(unsafe.Pointer(&handle)))
		if r != 0 {
			return nil, fmt.Errorf("PowerRegisterSuspendResumeNotification: %w", windows.Errno(r))
		}
		return func() {
			procUnregister.Call(handle)
			_ = params // keep the registration parameters alive until released
		}, nil
	})
}
