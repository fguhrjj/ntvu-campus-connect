//go:build windows

package config

import (
	"encoding/base64"
	"unsafe"

	"golang.org/x/sys/windows"
)

// dpapiEntropy binds ciphertext to this program on top of the user scope.
var dpapiEntropy = []byte("CampusConnect/v1")

type dpapiProtector struct{}

// NewProtector returns the CurrentUser-scoped DPAPI protector.
func NewProtector() Protector { return dpapiProtector{} }

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func takeBlob(out *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
}

func (dpapiProtector) Protect(plain []byte) (string, error) {
	var out windows.DataBlob
	err := windows.CryptProtectData(blob(plain), nil, blob(dpapiEntropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(takeBlob(&out)), nil
}

func (dpapiProtector) Unprotect(encoded string) ([]byte, error) {
	ct, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var out windows.DataBlob
	err = windows.CryptUnprotectData(blob(ct), nil, blob(dpapiEntropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return nil, err
	}
	return takeBlob(&out), nil
}
