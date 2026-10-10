//go:build windows

package wlan

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	clientVersion             = 2   // Vista and later
	opcodeCurrentConnection   = 7   // wlan_intf_opcode_current_connection
	stateConnected            = 1   // wlan_interface_state_connected
	sourceNone                = 0   // WLAN_NOTIFICATION_SOURCE_NONE
	sourceACM                 = 0x8 // WLAN_NOTIFICATION_SOURCE_ACM
	acmConnectionComplete     = 10  // wlan_notification_acm_connection_complete
	interfaceInfoSize         = 16 + 256*2 + 4
	connectionAttributesSSIDs = 4 + 4 + 256*2 // offset of DOT11_SSID
)

var (
	wlanapi                 = windows.NewLazySystemDLL("wlanapi.dll")
	procOpenHandle          = wlanapi.NewProc("WlanOpenHandle")
	procCloseHandle         = wlanapi.NewProc("WlanCloseHandle")
	procEnumInterfaces      = wlanapi.NewProc("WlanEnumInterfaces")
	procQueryInterface      = wlanapi.NewProc("WlanQueryInterface")
	procFreeMemory          = wlanapi.NewProc("WlanFreeMemory")
	procRegisterNotifcation = wlanapi.NewProc("WlanRegisterNotification")

	callback = windows.NewCallback(func(data *notificationData, context uintptr) uintptr {
		if data.Source == sourceACM && data.Code == acmConnectionComplete && data.Data != nil &&
			data.DataSize >= uint32(unsafe.Sizeof(connectionNotification{})) {
			handleConnectionComplete(context, (*connectionNotification)(data.Data))
		}
		return 0
	})
)

// notificationData is WLAN_NOTIFICATION_DATA.
type notificationData struct {
	Source        uint32
	Code          uint32
	InterfaceGUID windows.GUID
	DataSize      uint32
	Data          unsafe.Pointer
}

func call(p *windows.LazyProc, name string, args ...uintptr) error {
	if r, _, _ := p.Call(args...); r != 0 {
		return fmt.Errorf("%s: %w", name, windows.Errno(r))
	}
	return nil
}

func open() (windows.Handle, error) {
	var negotiated uint32
	var h windows.Handle
	err := call(procOpenHandle, "WlanOpenHandle", clientVersion, 0, uintptr(unsafe.Pointer(&negotiated)), uintptr(unsafe.Pointer(&h)))
	return h, err
}

func closeHandle(h windows.Handle) { procCloseHandle.Call(uintptr(h), 0) }

// CurrentSSID returns the SSID of the first connected WLAN interface, ""
// when none is connected, or an error (ErrSSIDUnavailable when withheld).
func CurrentSSID() (string, error) {
	h, err := open()
	if err != nil {
		return "", err
	}
	defer closeHandle(h)
	var list unsafe.Pointer
	if err := call(procEnumInterfaces, "WlanEnumInterfaces", uintptr(h), 0, uintptr(unsafe.Pointer(&list))); err != nil {
		return "", err
	}
	defer procFreeMemory.Call(uintptr(list))
	count := *(*uint32)(list)
	var firstErr error
	for i := range uintptr(count) {
		info := unsafe.Add(list, 8+i*interfaceInfoSize) // WLAN_INTERFACE_INFO_LIST header is two DWORDs
		if *(*uint32)(unsafe.Add(info, 16+256*2)) != stateConnected {
			continue
		}
		ssid, err := querySSID(h, (*windows.GUID)(info))
		if err == nil {
			return ssid, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", firstErr
}

func querySSID(h windows.Handle, guid *windows.GUID) (string, error) {
	var size, valueType uint32
	var data unsafe.Pointer
	if err := call(procQueryInterface, "WlanQueryInterface", uintptr(h), uintptr(unsafe.Pointer(guid)), opcodeCurrentConnection, 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&valueType))); err != nil {
		return "", err
	}
	defer procFreeMemory.Call(uintptr(data))
	if size < connectionAttributesSSIDs+4+maxSSIDLen {
		return "", fmt.Errorf("%w: short attributes", ErrSSIDUnavailable)
	}
	n := *(*uint32)(unsafe.Add(data, connectionAttributesSSIDs))
	return decodeSSID(n, (*[maxSSIDLen]byte)(unsafe.Add(data, connectionAttributesSSIDs+4)))
}

// Subscribe calls onConnected with the SSID ("" if withheld) after each
// successful WLAN connection. onConnected runs on an OS thread and must not block.
func Subscribe(ctx context.Context, onConnected func(ssid string)) (func(), error) {
	return registry.Subscribe(ctx, onConnected, func(id uintptr) (func(), error) {
		h, err := open()
		if err != nil {
			return nil, err
		}
		var prev uint32
		if err := call(procRegisterNotifcation, "WlanRegisterNotification", uintptr(h), sourceACM, 1, callback, id, 0, uintptr(unsafe.Pointer(&prev))); err != nil {
			closeHandle(h)
			return nil, err
		}
		return func() {
			procRegisterNotifcation.Call(uintptr(h), sourceNone, 1, 0, 0, 0, uintptr(unsafe.Pointer(&prev)))
			closeHandle(h)
		}, nil
	})
}
