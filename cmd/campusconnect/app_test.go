package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"campusconnect/internal/config"
	"campusconnect/internal/flow"
	"campusconnect/internal/logx"
	"campusconnect/internal/netguard"
)

// fakeNetwork answers like the campus portal for a signed-out user whose
// workflow waits at the authenticate node, and records every request that
// reaches the wire.
type fakeNetwork struct {
	mu   sync.Mutex
	reqs []string // "METHOD host/path"
}

const fakeSessionID = "sess-0123456789abcdef"

func (f *fakeNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r.Method+" "+r.URL.Host+r.URL.Path)
	f.mu.Unlock()
	if r.Body != nil {
		r.Body.Close()
	}
	resp := &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: http.NoBody, Request: r}
	if r.URL.Host != "nap.ntvu.edu.cn" {
		return resp, nil
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /":
		resp.StatusCode = http.StatusFound
		resp.Header.Set("Location", "https://nap.ntvu.edu.cn/eportal/redirect.jsp")
	case "GET /eportal/redirect.jsp":
		resp.StatusCode = http.StatusFound
		resp.Header.Set("Location", "http://nap.ntvu.edu.cn/portal/portal-main?sessionId="+fakeSessionID+
			"&userIp=10.1.2.3&userMac=aabbccddeeff&ssid=NTVU-R&nasIp=10.0.0.1&customPageId=4")
	case "POST /eportal/network/userOnline":
		resp.StatusCode, resp.Body = http.StatusOK, envelope(`{"online":false}`)
	case "POST /eportal/workFlow/getCurrentNode":
		resp.StatusCode, resp.Body = http.StatusOK, envelope(`{"currentNodePath":"authenticate"}`)
	}
	return resp, nil
}

func envelope(data string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(`{"code":200,"message":null,"data":` + data + `}`))
}

func (f *fakeNetwork) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

// newTestWiring builds the entry wiring on a fake network with a missing
// config file, a log in a temp dir and a recorded autostart setter.
func newTestWiring(t *testing.T, network http.RoundTripper) (*wiring, *[]bool) {
	t.Helper()
	dir := t.TempDir()
	log, err := logx.NewLogger(filepath.Join(dir, "log", "campus.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	var autostart []bool
	w := &wiring{
		configPath:   filepath.Join(dir, "config", "config.json"),
		log:          log,
		transport:    network,
		currentSSID:  func() (string, error) { return "NTVU-R", nil },
		setAutostart: func(on bool) error { autostart = append(autostart, on); return nil },
	}
	return w, &autostart
}

func TestAppWiringUsesProductionPolicy(t *testing.T) {
	network := &fakeNetwork{}
	w, _ := newTestWiring(t, network)
	client := w.httpClient()

	for _, blocked := range []struct{ method, url string }{
		{http.MethodPost, "https://nap.ntvu.edu.cn/eportal/network/offline"},
		{http.MethodGet, "https://nap.ntvu.edu.cn/cas-sso/logout"},
		{http.MethodGet, "https://example.com/"},
	} {
		req, err := http.NewRequest(blocked.method, blocked.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Do(req); !errors.Is(err, netguard.ErrBlocked) {
			t.Errorf("%s %s: err = %v, want ErrBlocked", blocked.method, blocked.url, err)
		}
	}
	if got := network.requests(); len(got) != 0 {
		t.Fatalf("blocked requests reached the transport: %v", got)
	}

	req, err := http.NewRequest(http.MethodGet, "https://nap.ntvu.edu.cn/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("allowlisted request: %v", err)
	}
	resp.Body.Close()
	if got := network.requests(); len(got) != 1 || got[0] != "GET nap.ntvu.edu.cn/" {
		t.Fatalf("transport saw %v, want only GET nap.ntvu.edu.cn/", got)
	}
}

func TestStartupWithoutConfigDoesNotAuthenticate(t *testing.T) {
	network := &fakeNetwork{}
	w, _ := newTestWiring(t, network)

	cfg, err := w.Load()
	if err != nil {
		t.Fatalf("Load with no config file: %v", err)
	}
	if cfg.Configured() {
		t.Fatalf("missing config reports configured: %+v", cfg)
	}

	res := w.connect(context.Background(), flow.ModeFull)
	if res.Status != flow.ConfigurationRequired {
		t.Fatalf("Connect = %+v, want ConfigurationRequired", res)
	}
	reqs := network.requests()
	reachedNode := false
	for _, r := range reqs {
		if strings.Contains(r, "authserver.ntvu.edu.cn") || strings.Contains(r, "/cas-sso/") || strings.HasSuffix(r, "/serviceLogin") {
			t.Errorf("unconfigured attempt sent %s", r)
		}
		reachedNode = reachedNode || r == "POST nap.ntvu.edu.cn/eportal/workFlow/getCurrentNode"
	}
	if !reachedNode {
		t.Fatalf("attempt never asked for the workflow node: %v", reqs)
	}
}

func TestConnectLogsSSIDAndEachRequest(t *testing.T) {
	w, _ := newTestWiring(t, &fakeNetwork{})
	w.connect(context.Background(), flow.ModeFull)

	b, err := os.ReadFile(w.log.Path())
	if err != nil {
		t.Fatal(err)
	}
	log := string(b)
	for _, want := range []string{
		"NTVU-R",
		"GET nap.ntvu.edu.cn/ 302",
		"GET nap.ntvu.edu.cn/eportal/redirect.jsp 302",
		"POST nap.ntvu.edu.cn/eportal/network/userOnline 200",
		"POST nap.ntvu.edu.cn/eportal/workFlow/getCurrentNode 200",
		"authenticate",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, fakeSessionID) {
		t.Errorf("log contains the portal session id:\n%s", log)
	}
}

func TestStoreSaveAppliesAutostart(t *testing.T) {
	w, autostart := newTestWiring(t, &fakeNetwork{})
	cfg := config.Default()
	cfg.Username = "20260001"
	cfg.Autostart = true
	if err := w.Save(cfg, ""); err != nil {
		t.Fatal(err)
	}
	cfg.Autostart = false
	if err := w.Save(cfg, ""); err != nil {
		t.Fatal(err)
	}
	if got := *autostart; len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("autostart setter calls = %v, want [true false]", got)
	}
	saved, err := w.Load()
	if err != nil || saved.Username != cfg.Username || saved.Autostart {
		t.Fatalf("Load after Save = %+v, %v", saved, err)
	}

	w.setAutostart = func(bool) error { return errors.New("registry denied") }
	if err := w.Save(cfg, ""); err == nil {
		t.Fatal("Save ignored the autostart failure")
	}
}

func TestStoreSaveAutostartFailureLeavesConfigUnchanged(t *testing.T) {
	w, _ := newTestWiring(t, &fakeNetwork{})
	cfg := config.Default()
	cfg.Username = "20260001"
	if err := w.Save(cfg, ""); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(w.configPath)
	if err != nil {
		t.Fatal(err)
	}

	w.setAutostart = func(bool) error { return errors.New("registry denied") }
	cfg.Username = "20269999"
	cfg.Autostart = true
	if err := w.Save(cfg, ""); err == nil {
		t.Fatal("Save ignored the autostart failure")
	}
	after, err := os.ReadFile(w.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("config changed despite the autostart failure:\n%s", after)
	}
}

// failingProtector cannot encrypt, so config.Save fails before writing.
type failingProtector struct{}

func (failingProtector) Protect([]byte) (string, error)   { return "", errors.New("DPAPI failed") }
func (failingProtector) Unprotect(string) ([]byte, error) { return nil, errors.New("DPAPI failed") }

func TestStoreSaveFailureRestoresAutostart(t *testing.T) {
	w, autostart := newTestWiring(t, &fakeNetwork{})
	cfg := config.Default()
	cfg.Username = "20260001"
	if err := w.Save(cfg, ""); err != nil {
		t.Fatal(err)
	}

	w.protector = failingProtector{}
	cfg.Autostart = true
	if err := w.Save(cfg, "new-password"); err == nil {
		t.Fatal("Save reported success although the config was not written")
	}
	if got := *autostart; len(got) != 3 || got[0] || !got[1] || got[2] {
		t.Fatalf("autostart setter calls = %v, want [false true false] (restored to the saved value)", got)
	}
	if saved, err := w.Load(); err != nil || saved.Autostart {
		t.Fatalf("Load after failed Save = %+v, %v", saved, err)
	}
}
