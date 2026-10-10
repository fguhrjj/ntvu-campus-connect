package portal

import (
	"context"
	"net/http"
	"net/url"
)

const (
	maxDiscoverHops = 3
	portalMainPath  = "/portal/portal-main"
)

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
		resp.Body.Close()
		loc, err := c.redirectTarget(current, resp, op)
		if err != nil {
			return Session{}, err
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
// stays on the portal host (http or https).
func (c *Client) redirectTarget(current *url.URL, resp *http.Response, op string) (*url.URL, error) {
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
	if (loc.Scheme != "http" && loc.Scheme != "https") || loc.Host != c.base.Host || loc.User != nil {
		return nil, &Error{Kind: KindChanged, Op: op, Message: "redirect leaves portal host: " + loc.Scheme + "://" + loc.Host}
	}
	return loc, nil
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
