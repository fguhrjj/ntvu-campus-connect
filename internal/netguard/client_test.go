package netguard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

type allowAll struct{}

func (allowAll) Allow(string, *url.URL) bool { return true }

type denyAll struct{}

func (denyAll) Allow(string, *url.URL) bool { return false }

func TestDoDoesNotFollowRedirect(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
	}))
	defer srv.Close()

	c := NewClient(allowAll{}, srv.Client().Transport, 7*time.Second)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "https://evil.example/steal" {
		t.Fatalf("Location = %q", got)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hits = %d, want 1", hits.Load())
	}
}

func TestDoRejectsBeforeSending(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	var events []Event
	c := NewClient(denyAll{}, srv.Client().Transport, 7*time.Second)
	c.Observe = func(e Event) { events = append(events, e) }
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/eportal/network/offline", nil)
	_, err := c.Do(req)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("blocked request reached server")
	}
	if len(events) != 1 || !events[0].Blocked || events[0].Path != "/eportal/network/offline" {
		t.Fatalf("events = %+v, want one blocked event with path", events)
	}
}

func TestClientKeepsCookiesPerClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: "abc", Path: "/"})
			return
		}
		if c, err := r.Cookie("SESSION"); err != nil || c.Value != "abc" {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	c := NewClient(allowAll{}, srv.Client().Transport, 7*time.Second)
	for _, p := range []string{"/set", "/check"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+p, nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("Do %s: %v", p, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200 (cookie not kept)", p, resp.StatusCode)
		}
	}

	fresh := NewClient(allowAll{}, srv.Client().Transport, 7*time.Second)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/check", nil)
	resp, err := fresh.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("fresh client reused cookies from another client")
	}
}

func TestDefaultTransportDisablesProxy(t *testing.T) {
	tr := DefaultTransport()
	if tr.Proxy != nil {
		t.Fatalf("Proxy must be nil so system proxies never see portal traffic")
	}
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("TLS verification must stay on")
	}
}
