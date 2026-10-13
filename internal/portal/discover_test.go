package portal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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

// newInterceptClient serves the portal from nap and, like the NAS does for a
// client it has not authorised, answers the intercept origin from trap.
func newInterceptClient(t *testing.T, nap, trap http.Handler) (*Client, *recorder, *httptest.Server) {
	t.Helper()
	napSrv := httptest.NewServer(nap)
	trapSrv := httptest.NewServer(trap)
	t.Cleanup(napSrv.Close)
	t.Cleanup(trapSrv.Close)
	var d net.Dialer
	tr := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == interceptHost+":80" {
			addr = trapSrv.Listener.Addr().String()
		}
		return d.DialContext(ctx, network, addr)
	}}
	t.Cleanup(tr.CloseIdleConnections)
	rec := &recorder{next: tr}
	base, _ := url.Parse(napSrv.URL)
	return NewClient(netguard.NewClient(allowAll{}, rec, 7*time.Second), base), rec, napSrv
}

const portalMainQuery = "sessionId=a1b2c3d4e5f6&userIp=10.18.124.235&userMac=0a0b0c0d0e0f&ssid=Ruijie&nasIp=10.255.2.250&customPageId=4&redirectUrl=http://123.123.123.123/&terminalType=pc&guestFlow=false&userOnline=false&authSuccessSource=portal"

// interceptQuery is the index.jsp query the NAS intercept page names (2026-10-13 offline capture shape).
const interceptQuery = "wlanuserip=10.18.124.235&wlanacname=NAS&ssid=Ruijie&nasip=10.255.2.250&mac=0a0b0c0d0e0f&t=wireless-v2-plain&url=http://123.123.123.123/"

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

func TestDiscoverFollowsNASInterceptPage(t *testing.T) {
	var napURL string
	nap := http.NewServeMux()
	nap.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, napURL+"/eportal/redirect.jsp", http.StatusFound)
	})
	nap.HandleFunc("/eportal/redirect.jsp", func(w http.ResponseWriter, r *http.Request) {
		// Offline: the portal sends the client to the bare intercept origin.
		w.Header().Set("Location", "http://"+interceptHost)
		w.WriteHeader(http.StatusFound)
	})
	nap.HandleFunc("/eportal/index.jsp", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != interceptQuery {
			t.Errorf("index.jsp query = %q, want the intercept script's", r.URL.RawQuery)
		}
		http.Redirect(w, r, napURL+"/portal/portal-main?"+portalMainQuery, http.StatusFound)
	})
	trap := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" || r.URL.RawQuery != "" {
			t.Errorf("intercept request = %s %s", r.Method, r.URL)
		}
		fmt.Fprintf(w, "<script>top.self.location.href='%s/eportal/index.jsp?%s'</script>", napURL, interceptQuery)
	})
	c, rec, srv := newInterceptClient(t, nap, trap)
	napURL = srv.URL

	s, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if s.ID != "a1b2c3d4e5f6" || s.UserMAC != "0a0b0c0d0e0f" {
		t.Fatalf("session = %+v", s)
	}
	host := strings.TrimPrefix(napURL, "http://")
	want := []string{"GET " + host + "/", "GET " + host + "/eportal/redirect.jsp", "GET " + interceptHost + "/", "GET " + host + "/eportal/index.jsp"}
	if strings.Join(rec.reqs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %q\nwant       %q", rec.reqs, want)
	}
}

func TestDiscoverRejectsInterceptScriptOffPortal(t *testing.T) {
	// NAP is replaced with the portal test server's URL.
	for name, page := range map[string]string{
		"other host":   "<script>top.self.location.href='https://evil.example/eportal/index.jsp?x=1'</script>",
		"relative":     "<script>top.self.location.href='/eportal/index.jsp?x=1'</script>",
		"unknown path": "<script>top.self.location.href='NAP/eportal/logout.jsp'</script>",
		"no script":    "<html><body>blocked</body></html>",
	} {
		t.Run(name, func(t *testing.T) {
			var napURL string
			nap := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://"+interceptHost)
				w.WriteHeader(http.StatusFound)
			})
			trap := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, strings.ReplaceAll(page, "NAP", napURL))
			})
			c, rec, srv := newInterceptClient(t, nap, trap)
			napURL = srv.URL
			_, err := c.Discover(context.Background())
			var pe *Error
			if !errors.As(err, &pe) || pe.Kind != KindChanged {
				t.Fatalf("err = %v, want KindChanged", err)
			}
			if len(rec.reqs) != 2 {
				t.Fatalf("requests = %v, want only / and the intercept page", rec.reqs)
			}
		})
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
		// Only the bare http intercept origin is followed.
		"intercept port":  "http://123.123.123.123:8080/",
		"intercept https": "https://123.123.123.123/",
		"other address":   "http://123.123.123.124/",
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

func TestDiscoverStopsAfterFourHops(t *testing.T) {
	c, rec, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/eportal/redirect.jsp")
		w.WriteHeader(http.StatusFound)
	}))
	_, err := c.Discover(context.Background())
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindChanged {
		t.Fatalf("err = %v, want KindChanged", err)
	}
	if len(rec.reqs) != 4 {
		t.Fatalf("requests = %d, want 4", len(rec.reqs))
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
