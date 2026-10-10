package wlan

import "testing"

func ssidBytes(s string) *[32]byte {
	var b [32]byte
	copy(b[:], s)
	return &b
}

func TestDecodeSSIDRejectsEmptyOrInvalidLength(t *testing.T) {
	for _, n := range []uint32{0, 33, 1 << 31} {
		if got, err := decodeSSID(n, ssidBytes("NTVU-R")); err == nil {
			t.Errorf("decodeSSID(%d) = %q, want error", n, got)
		}
	}
}

func TestDecodeSSIDPreservesSSIDBytes(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		n    uint32
		want string
	}{
		{"NTVU-R", 6, "NTVU-R"},
		{"NTVU-R 2", 8, "NTVU-R 2"},       // profile-like suffix stays part of the SSID
		{"NTVU-Rxxxx", 6, "NTVU-R"},       // length, not NUL, ends the SSID
		{"a\x00b", 3, "a\x00b"},           // embedded NUL kept
		{"\xff\xfeGBK", 5, "\xff\xfeGBK"}, // non-UTF-8 bytes untouched
		{string(make([]byte, 32)), 32, string(make([]byte, 32))},
	} {
		got, err := decodeSSID(tc.n, ssidBytes(tc.raw))
		if err != nil || got != tc.want {
			t.Errorf("decodeSSID(%d, %q) = %q, %v; want %q", tc.n, tc.raw, got, err, tc.want)
		}
	}
}

func TestConnectedNotificationDeliversSSIDOnlyOnSuccess(t *testing.T) {
	var got []string
	var id uintptr
	unsubscribe, err := registry.Subscribe(t.Context(), func(s string) { got = append(got, s) }, func(i uintptr) (func(), error) {
		id = i
		return func() {}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()

	ok := connectionNotification{SSIDLength: 6, SSID: *ssidBytes("NTVU-R")}
	failed := ok
	failed.ReasonCode = 0x30005        // any non-success reason
	hidden := connectionNotification{} // SSID withheld (no location permission)

	handleConnectionComplete(id, &failed)
	handleConnectionComplete(id, &ok)
	handleConnectionComplete(id, &hidden)
	if len(got) != 2 || got[0] != "NTVU-R" || got[1] != "" {
		t.Fatalf("delivered = %q, want [NTVU-R \"\"]", got)
	}
}
