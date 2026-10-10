package portal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"campusconnect/internal/netguard"
)

type allowAll struct{}

func (allowAll) Allow(string, *url.URL) bool { return true }

// recorder wraps a transport and records every request that leaves it.
type recorder struct {
	next http.RoundTripper
	mu   sync.Mutex
	reqs []string
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req.Method+" "+req.URL.Host+req.URL.Path)
	r.mu.Unlock()
	return r.next.RoundTrip(req)
}

func newTestClient(t *testing.T, h http.Handler) (*Client, *recorder, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	rec := &recorder{next: srv.Client().Transport}
	base, _ := url.Parse(srv.URL)
	return NewClient(netguard.NewClient(allowAll{}, rec, 7*time.Second), base), rec, srv
}

const portalMainQuery = "sessionId=a1b2c3d4e5f6&userIp=10.18.124.235&userMac=0a0b0c0d0e0f&ssid=Ruijie&nasIp=10.255.2.250&customPageId=4&redirectUrl=http://123.123.123.123/&terminalType=pc&guestFlow=false&userOnline=false&authSuccessSource=portal"

func TestDiscoverParsesFreshPortalSession(t *testing.T) {
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Location", srvURL+"/eportal/redirect.jsp")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/eportal/redirect.jsp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", srvURL+"/portal/portal-main?"+portalMainQuery)
		w.WriteHeader(http.StatusFound)
	})
	c, rec, srv := newTestClient(t, mux)
	srvURL = srv.URL

	s, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := Session{ID: "a1b2c3d4e5f6", UserIP: "10.18.124.235", UserMAC: "0a0b0c0d0e0f", SSID: "Ruijie", NASIP: "10.255.2.250", CustomPageID: "4", AdvertisedOnline: false}
	if s != want {
		t.Fatalf("session = %+v\nwant      %+v", s, want)
	}
	if len(rec.reqs) != 2 {
		t.Fatalf("requests = %v, want exactly / and redirect.jsp (portal-main must not be fetched)", rec.reqs)
	}
}

func TestDiscoverReadsAdvertisedOnline(t *testing.T) {
	var srvURL string
	c, _, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", srvURL+"/portal/portal-main?sessionId=abc&userOnline=true")
		w.WriteHeader(http.StatusFound)
	}))
	srvURL = srv.URL
	s, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !s.AdvertisedOnline || s.ID != "abc" {
		t.Fatalf("session = %+v, want online abc", s)
	}
}

func TestDiscoverRejectsUnexpectedRedirectHost(t *testing.T) {
	// HOST is replaced with the test server's host:port, NAME/PORT with its parts.
	for name, loc := range map[string]string{
		"suffix spoof":  "http://NAME.evil.example:PORT/eportal/redirect.jsp",
		"other host":    "https://evil.example/portal/portal-main?sessionId=abc",
		"unknown path":  "http://HOST/portal/device-unregistered",
		"ftp scheme":    "ftp://HOST/eportal/redirect.jsp",
		"userinfo":      "http://user@HOST/eportal/redirect.jsp",
		"missing state": "http://HOST/portal/portal-main?userIp=1.2.3.4",
	} {
		t.Run(name, func(t *testing.T) {
			var u *url.URL
			c, rec, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				rep := strings.NewReplacer("HOST", u.Host, "NAME", u.Hostname(), "PORT", u.Port())
				w.Header().Set("Location", rep.Replace(loc))
				w.WriteHeader(http.StatusFound)
			}))
			u, _ = url.Parse(srv.URL)
			_, err := c.Discover(context.Background())
			var pe *Error
			if !errors.As(err, &pe) || pe.Kind != KindChanged {
				t.Fatalf("err = %v, want KindChanged", err)
			}
			if len(rec.reqs) != 1 {
				t.Fatalf("requests = %v, want only the first hop", rec.reqs)
			}
		})
	}
}

func TestDiscoverStopsAfterThreeHops(t *testing.T) {
	c, rec, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/eportal/redirect.jsp")
		w.WriteHeader(http.StatusFound)
	}))
	_, err := c.Discover(context.Background())
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindChanged {
		t.Fatalf("err = %v, want KindChanged", err)
	}
	if len(rec.reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(rec.reqs))
	}
}

func TestDiscoverServerErrorIsUnavailable(t *testing.T) {
	c, _, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	_, err := c.Discover(context.Background())
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindUnavailable {
		t.Fatalf("err = %v, want KindUnavailable", err)
	}
}
