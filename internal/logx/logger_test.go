package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLoggerRedactsSensitiveQueryAndFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "campus.log")
	l, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.SetUsername("20230001234")
	l.Request("GET", "authserver.ntvu.edu.cn/authserver/login?service=https%3A%2F%2Fnap&delegatedclientid=TST-1-x", 200, 120*time.Millisecond)
	l.Event("Location https://nap.ntvu.edu.cn/cas-sso/login?client_name=adapter&ticket=ST-12345-abcdef&delegatedclientid=TST-99")
	l.Event("portal sessionId=a1b2c3d4e5f6 userIndex=ZXhhbXBsZQ execution=e1s1 pwdEncryptSalt=feINsdNKS0tk5gxb")
	l.Event(`json {"sessionId":"a1b2c3d4e5f6","password":"Q2lwaGVy","userIndex":"ZXhhbXBsZQ"}`)
	l.Event("Cookie: SESSION=5fcefde8-2359; JSESSIONID=ABCDEF0123")
	l.Event("Set-Cookie: route=abc123; Path=/")
	l.Event("账号 20230001234 登录 userIp=10.18.124.235 userMac=0a0b0c0d0e0f")

	got := readLog(t, path)
	for _, secret := range []string{"TST-1-x", "ST-12345-abcdef", "TST-99", "a1b2c3d4e5f6", "ZXhhbXBsZQ", "e1s1", "feINsdNKS0tk5gxb", "Q2lwaGVy", "5fcefde8", "ABCDEF0123", "abc123", "20230001234", "%2F%2Fnap"} {
		if strings.Contains(got, secret) {
			t.Errorf("log contains %q:\n%s", secret, got)
		}
	}
	for _, keep := range []string{"GET authserver.ntvu.edu.cn/authserver/login 200 120ms", "client_name", "ticket=", "***1234", "10.18.124.235", "0a0b0c0d0e0f"} {
		if !strings.Contains(got, keep) {
			t.Errorf("log lacks %q:\n%s", keep, got)
		}
	}
}

func TestMaskUsername(t *testing.T) {
	for in, want := range map[string]string{"20230001234": "***1234", "abc": "***", "": ""} {
		if got := MaskUsername(in); got != want {
			t.Errorf("MaskUsername(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoggerRotatesAtOneMiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "campus.log")
	l, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 1000)
	for range 1100 {
		l.Event(line)
	}
	l.Event("after rotation")
	l.Close()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() >= 1<<20 {
		t.Fatalf("current log is %d bytes, want rotated below 1 MiB", st.Size())
	}
	old, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	if old.Size() < 1<<20-2000 {
		t.Fatalf("rotated file is %d bytes", old.Size())
	}
	if !strings.Contains(readLog(t, path), "after rotation") {
		t.Fatal("new entries not in current log")
	}
	// Only one old generation is kept.
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Fatalf("unexpected second generation: %v", err)
	}
}

func TestLoggerKeepsWritingWhenRotationFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "campus.log")
	// A non-empty directory at <path>.1 makes the rename fail on every OS.
	if err := os.MkdirAll(filepath.Join(path+".1", "keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 1000)
	for range 1100 {
		l.Event(line)
	}
	l.Event("after failed rotation")
	for range 200 {
		l.Event(line)
	}
	l.Event("still logging")
	l.Close()
	got := readLog(t, path)
	for _, want := range []string{"after failed rotation", "still logging"} {
		if !strings.Contains(got, want) {
			t.Errorf("current log lacks %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(path+".1", "keep")); err != nil {
		t.Fatalf("existing %s.1 was disturbed: %v", path, err)
	}
}

func TestLoggerAppendsAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "campus.log")
	l, _ := NewLogger(path)
	l.Event("first run")
	l.Close()
	l, _ = NewLogger(path)
	l.Event("second run")
	l.Close()
	got := readLog(t, path)
	if !strings.Contains(got, "first run") || !strings.Contains(got, "second run") {
		t.Fatalf("log = %q", got)
	}
}
