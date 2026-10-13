// Package netguard is the only way the program talks to the network. Every
// request is checked against an allowlist before it is sent and redirects are
// never followed automatically, so callers must validate each Location hop.
package netguard

import (
	"net/http"
	"net/url"
)

// Policy decides whether a request may leave the machine.
type Policy interface {
	Allow(method string, u *url.URL) bool
}

type endpoint struct {
	scheme, host, path string
	methods            []string
	query              func(url.Values) bool
}

type allowlist []endpoint

const (
	napHost   = "nap.ntvu.edu.cn"
	authHost  = "authserver.ntvu.edu.cn"
	probeHost = "www.msftconnecttest.com"
	// interceptHost answers for every HTTP site until the portal login is done;
	// redirect.jsp sends unauthorised clients to its bare origin.
	interceptHost = "123.123.123.123"
)

// ProductionPolicy is the request allowlist from spec §2.5. Everything else,
// including every logout/offline endpoint, is rejected before sending.
func ProductionPolicy() Policy {
	get := []string{http.MethodGet}
	post := []string{http.MethodPost}
	return allowlist{
		{"https", napHost, "/", get, nil},
		{"https", napHost, "/eportal/redirect.jsp", get, nil},
		{"http", interceptHost, "/", get, noQuery},
		{"https", napHost, "/eportal/index.jsp", get, nil},
		{"https", napHost, "/eportal/network/userOnline", post, nil},
		{"https", napHost, "/eportal/adaptor/getOnlineUserInfo", get, nil},
		{"https", napHost, "/eportal/workFlow/getCurrentNode", post, nil},
		{"https", napHost, "/eportal/network/serviceSelection", post, nil},
		{"https", napHost, "/eportal/network/serviceLogin", post, nil},
		{"https", napHost, "/cas-sso/login", get, nil},
		{"https", napHost, "/cas-sso/clientredirect", get, adapterOnly},
		{"https", authHost, "/authserver/login", []string{http.MethodGet, http.MethodPost}, nil},
		{"https", authHost, "/authserver/checkNeedCaptcha.htl", get, nil},
		{"http", probeHost, "/connecttest.txt", get, nil},
	}
}

func adapterOnly(q url.Values) bool {
	v := q["client_name"]
	return len(v) == 1 && v[0] == "adapter"
}

func noQuery(q url.Values) bool { return len(q) == 0 }

func (a allowlist) Allow(method string, u *url.URL) bool {
	if u == nil || u.User != nil || u.Opaque != "" || u.Fragment != "" || u.Port() != "" {
		return false
	}
	// Reject paths whose wire form differs from the decoded form (%2F, dot
	// segments encoded, ...), so the server sees exactly the listed path.
	if u.EscapedPath() != u.Path {
		return false
	}
	for _, e := range a {
		if u.Scheme != e.scheme || u.Hostname() != e.host || u.Path != e.path {
			continue
		}
		for _, m := range e.methods {
			if m == method {
				return e.query == nil || e.query(u.Query())
			}
		}
		return false
	}
	return false
}
