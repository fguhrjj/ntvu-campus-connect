// Package wlan reads the connected SSID and ACM connection-complete events
// from wlanapi.dll (spec §4: never NetConnectionProfile.Name).
package wlan

import (
	"errors"
	"fmt"

	"campusconnect/internal/notify"
)

// ErrUnsupported is returned off Windows.
var ErrUnsupported = errors.New("wlan: requires Windows")

// ErrSSIDUnavailable means a WLAN is connected but Windows withheld the SSID
// (for example location access is denied). Callers continue as "unknown".
var ErrSSIDUnavailable = errors.New("wlan: SSID unavailable")

const maxSSIDLen = 32

// decodeSSID returns the raw SSID bytes; SSIDs are octet strings, not text.
func decodeSSID(n uint32, b *[maxSSIDLen]byte) (string, error) {
	if n == 0 || n > maxSSIDLen {
		return "", fmt.Errorf("%w: length %d", ErrSSIDUnavailable, n)
	}
	return string(b[:n]), nil
}

// connectionNotification is the fixed prefix of WLAN_CONNECTION_NOTIFICATION_DATA
// (DOT11_SSID inlined; the trailing profile XML is not read).
type connectionNotification struct {
	ConnectionMode  uint32
	ProfileName     [256]uint16
	SSIDLength      uint32
	SSID            [maxSSIDLen]byte
	BSSType         uint32
	SecurityEnabled int32
	ReasonCode      uint32
	Flags           uint32
}

var registry notify.Registry[string]

// handleConnectionComplete delivers the SSID of a successful connection;
// "" when Windows withheld it.
func handleConnectionComplete(id uintptr, n *connectionNotification) {
	if n.ReasonCode != 0 { // WLAN_REASON_CODE_SUCCESS
		return
	}
	ssid, err := decodeSSID(n.SSIDLength, &n.SSID)
	if err != nil {
		ssid = ""
	}
	registry.Deliver(id, ssid)
}
