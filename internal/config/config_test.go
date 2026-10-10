package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeProtector reverses bytes; enough to prove Save/Load call it.
type fakeProtector struct{ calls int }

func (f *fakeProtector) Protect(b []byte) (string, error) {
	f.calls++
	return "fake:" + reverse(string(b)), nil
}

func (f *fakeProtector) Unprotect(s string) ([]byte, error) {
	if !strings.HasPrefix(s, "fake:") {
		return nil, errors.New("not fake ciphertext")
	}
	return []byte(reverse(strings.TrimPrefix(s, "fake:"))), nil
}

func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func TestLoadMissingFileReturnsUnconfiguredDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "config.json"), &fakeProtector{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Configured() {
		t.Fatal("missing config reported as configured")
	}
	if cfg.ServiceValue != "CUCC" || cfg.ServiceLabel != "中国联通" || cfg.SSID != "NTVU-R" || cfg.SchemaVersion != 1 {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestSaveThenLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	p := &fakeProtector{}
	in := Default()
	in.Username = "20230001"
	in.Autostart = true
	if err := Save(path, in, "pw 密码", p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "pw 密码") {
		t.Fatal("plaintext password written to disk")
	}
	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("config is not JSON: %v", err)
	}
	for _, k := range []string{"schemaVersion", "username", "passwordProtected", "serviceValue", "serviceLabel", "ssid", "autostart"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("config lacks key %q", k)
		}
	}
	out, err := Load(path, p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pw, err := out.Password(p)
	if err != nil || pw != "pw 密码" {
		t.Fatalf("Password = %q, %v", pw, err)
	}
	if !out.Configured() || out.Username != "20230001" || !out.Autostart {
		t.Fatalf("loaded = %+v", out)
	}
}

func TestSaveBlankPasswordPreservesCiphertext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	p := &fakeProtector{}
	cfg := Default()
	cfg.Username = "20230001"
	if err := Save(path, cfg, "first", p); err != nil {
		t.Fatal(err)
	}
	// The UI edits a copy that may not carry the ciphertext.
	edited := Default()
	edited.Username = "20230001"
	edited.ServiceValue, edited.ServiceLabel = "CMCC", "中国移动"
	if err := Save(path, edited, "", p); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path, p)
	if err != nil {
		t.Fatal(err)
	}
	if pw, _ := out.Password(p); pw != "first" {
		t.Fatalf("password after blank save = %q, want preserved", pw)
	}
	if out.ServiceValue != "CMCC" {
		t.Fatalf("service = %q", out.ServiceValue)
	}
	if p.calls != 1 {
		t.Fatalf("Protect calls = %d, want 1", p.calls)
	}
}

func TestLoadRejectsUnsupportedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"schemaVersion":2,"username":"x"}`), 0o600)
	if _, err := Load(path, &fakeProtector{}); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("err = %v, want unsupported schema", err)
	}
}

func TestLoadRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"schemaVersion":1,`), 0o600)
	if _, err := Load(path, &fakeProtector{}); err == nil {
		t.Fatal("corrupt config accepted")
	}
}

func TestProtectRoundTrip(t *testing.T) {
	p := NewProtector()
	ct, err := p.Protect([]byte("秘密 secret"))
	if runtime.GOOS != "windows" {
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("non-Windows Protect err = %v, want ErrUnsupported", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("Protect: %v", err)
	}
	if strings.Contains(ct, "secret") {
		t.Fatal("ciphertext contains plaintext")
	}
	pt, err := p.Unprotect(ct)
	if err != nil || string(pt) != "秘密 secret" {
		t.Fatalf("Unprotect = %q, %v", pt, err)
	}
	if _, err := p.Unprotect("bm90IGRwYXBp"); err == nil {
		t.Fatal("Unprotect accepted non-DPAPI data")
	}
}
