package portal

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
)

const (
	// Online: / → redirect.jsp → portal-main. Not yet authorised: / →
	// redirect.jsp → NAS intercept page → index.jsp → portal-main.
	maxDiscoverHops = 4
	portalMainPath  = "/portal/portal-main"
	indexPath       = "/eportal/index.jsp"
	// interceptHost is the NAS address redirect.jsp names while the client is
	// not yet authorised; its page script points back at /eportal/index.jsp.
	interceptHost    = "123.123.123.123"
	maxInterceptPage = 64 << 10
)

var interceptScript = regexp.MustCompile(`location\.href\s*=\s*['"]([^'"]+)['"]`)

// Discover gets a fresh portal session by following the entry redirects by
// hand (spec §2.1). The portal-main URL is parsed but never fetched.
func (c *Client) Discover(ctx context.Context) (Session, error) {
	const op = "discover"
	current := c.endpoint("/", nil)
	for range maxDiscoverHops {
		req, err := http.NewRequest(http.MethodGet, current.String(), nil)
		if err != nil {
			return Session{}, &Error{Kind: KindChanged, Op: op, Err: err}
		}
		resp, err := c.http.Do(req.WithContext(ctx))
		if err != nil {
			return Session{}, &Error{Kind: KindUnavailable, Op: op, Err: err}
		}
		if current.Host == interceptHost {
			if current, err = c.interceptTarget(resp, op); err != nil {
				return Session{}, err
			}
			continue
		}
		resp.Body.Close()
		loc, err := c.redirectTarget(resp, op)
		if err != nil {
			return Session{}, err
		}
		if isIntercept(loc) {
			current = &url.URL{Scheme: "http", Host: interceptHost, Path: "/"}
			continue
		}
		switch loc.Path {
		case portalMainPath:
			return parseSession(loc.Query(), op)
		case "/", "/eportal/redirect.jsp":
			// Stay on the configured origin: only the final portal-main hop is http.
			next := c.endpoint(loc.Path, nil)
			next.RawQuery = loc.RawQuery
			current = next
		default:
			return Session{}, &Error{Kind: KindChanged, Op: op, Message: "unexpected redirect path " + loc.Path}
		}
	}
	return Session{}, &Error{Kind: KindChanged, Op: op, Message: "too many redirects"}
}

// redirectTarget validates one redirect hop: it must be a 3xx whose Location
// stays on the portal host (http or https) or is the bare intercept origin.
func (c *Client) redirectTarget(resp *http.Response, op string) (*url.URL, error) {
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect:
	default:
		if resp.StatusCode >= 500 {
			return nil, statusError(op, resp.StatusCode)
		}
		return nil, &Error{Kind: KindChanged, Op: op, Message: "expected redirect, got " + resp.Status}
	}
	loc, err := resp.Location()
	if err != nil {
		return nil, &Error{Kind: KindChanged, Op: op, Message: "redirect without valid Location", Err: err}
	}
	if isIntercept(loc) {
		return loc, nil
	}
	if (loc.Scheme != "http" && loc.Scheme != "https") || loc.Host != c.base.Host || loc.User != nil {
		return nil, &Error{Kind: KindChanged, Op: op, Message: "redirect leaves portal host: " + loc.Scheme + "://" + loc.Host}
	}
	return loc, nil
}

// isIntercept reports whether loc is exactly http://123.123.123.123[/].
func isIntercept(loc *url.URL) bool {
	return loc.Scheme == "http" && loc.Host == interceptHost && loc.User == nil &&
		(loc.Path == "" || loc.Path == "/") && loc.RawQuery == ""
}

// interceptTarget reads the NAS intercept page and returns the portal
// index.jsp URL its script names, rebuilt on the configured origin. Anything
// else on the page is ignored; any other target is rejected.
func (c *Client) interceptTarget(resp *http.Response, op string) (*url.URL, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(op, resp.StatusCode)
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, maxInterceptPage))
	if err != nil {
		return nil, &Error{Kind: KindUnavailable, Op: op, Err: err}
	}
	m := interceptScript.FindSubmatch(page)
	if m == nil {
		return nil, &Error{Kind: KindChanged, Op: op, Message: "intercept page names no portal URL"}
	}
	loc, err := url.Parse(string(m[1]))
	if err != nil || (loc.Scheme != "http" && loc.Scheme != "https") || loc.Host != c.base.Host || loc.User != nil || loc.Path != indexPath {
		return nil, &Error{Kind: KindChanged, Op: op, Message: "intercept page leaves the portal entry"}
	}
	next := c.endpoint(indexPath, nil)
	next.RawQuery = loc.RawQuery
	return next, nil
}

func parseSession(q url.Values, op string) (Session, error) {
	s := Session{
		ID:               q.Get("sessionId"),
		UserIP:           q.Get("userIp"),
		UserMAC:          q.Get("userMac"),
		SSID:             q.Get("ssid"),
		NASIP:            q.Get("nasIp"),
		CustomPageID:     q.Get("customPageId"),
		AdvertisedOnline: q.Get("userOnline") == "true",
	}
	if s.ID == "" {
		return Session{}, &Error{Kind: KindChanged, Op: op, Message: "portal-main redirect has no sessionId"}
	}
	return s, nil
}
