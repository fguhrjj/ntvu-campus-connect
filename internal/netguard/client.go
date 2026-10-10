package netguard

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"time"
)

// ErrBlocked is returned when a request is outside the allowlist.
var ErrBlocked = errors.New("netguard: request blocked by allowlist")

// UserAgent is a common desktop browser string (spec §4).
const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0"

// Event describes one request for logging. It never carries the query
// string, headers or bodies.
type Event struct {
	Method  string
	Host    string
	Path    string
	Status  int
	Elapsed time.Duration
	Blocked bool
	Err     error
}

// Client sends allowlisted requests with its own cookie jar and never
// follows redirects.
type Client struct {
	policy Policy
	http   *http.Client
	// Observe, when set, receives every attempted request.
	Observe func(Event)
}

// DefaultTransport is the production transport: system proxies are ignored
// and TLS verification stays on.
func DefaultTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return t
}

// NewClient returns a client with an empty cookie jar. Use one client per
// connection attempt so cookies never outlive it.
func NewClient(policy Policy, transport http.RoundTripper, timeout time.Duration) *Client {
	jar, _ := cookiejar.New(nil) // never fails with nil options
	return &Client{
		policy: policy,
		http: &http.Client{
			Transport: transport,
			Jar:       jar,
			Timeout:   timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Do checks req against the policy and sends it. A 3xx response is returned
// to the caller unchanged.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	ev := Event{Method: req.Method, Host: req.URL.Host, Path: req.URL.Path}
	if !c.policy.Allow(req.Method, req.URL) {
		ev.Blocked = true
		ev.Err = fmt.Errorf("%w: %s %s%s", ErrBlocked, req.Method, req.URL.Host, req.URL.Path)
		c.observe(ev)
		return nil, ev.Err
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	ev.Elapsed = time.Since(start)
	if err != nil {
		ev.Err = err
	} else {
		ev.Status = resp.StatusCode
	}
	c.observe(ev)
	return resp, err
}

func (c *Client) observe(e Event) {
	if c.Observe != nil {
		c.Observe(e)
	}
}
