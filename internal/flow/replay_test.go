package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"campusconnect/internal/cas"
	"campusconnect/internal/netguard"
	"campusconnect/internal/portal"
)

// Spec §2 sample shapes, the same ones replayed by the internal/portal and
// internal/cas tests.
const (
	replaySessionID = "a1b2c3d4e5f6"
	replayUserMAC   = "0a0b0c0d0e0f"
	// portalMainQuery takes the userOnline flag.
	portalMainQuery = "sessionId=a1b2c3d4e5f6&userIp=10.18.124.235&userMac=0a0b0c0d0e0f&ssid=Ruijie&nasIp=10.255.2.250&customPageId=4&redirectUrl=http://123.123.123.123/&terminalType=pc&guestFlow=false&userOnline=%t&authSuccessSource=portal"

	userOnlineTrue  = `{"code":200,"message":null,"data":{"sessionId":"a1b2c3d4e5f6","online":true,"userIndex":"x","message":"redirect success"}}`
	userOnlineFalse = `{"code":200,"message":null,"data":{"sessionId":"a1b2c3d4e5f6","online":false,"userIndex":"","portalSuccessUrl":null}}`
	// currentNodeReply takes currentNodePath.
	currentNodeReply  = `{"code":200,"message":null,"data":{"currentNodeId":"n","sessionId":"a1b2c3d4e5f6","flowKey":"portal_auth","currentNodePath":"%s","flowSessionInfo":null,"userinfo":{}}}`
	serviceListReply  = `{"code":200,"message":null,"data":[{"key":"校园网","value":"PUBLIC"},{"key":"中国联通","value":"CUCC"}]}`
	serviceLoginReply = `{"code":200,"message":null,"data":{"authResult":"success","authMessage":""}}`
	sessionGoneReply  = `{"code":500,"message":"会话不存在","data":null}`

	adapterLoginHTML = `<html><body><p id="current-login-type">adapter</p><p id="login-error-code"></p></body></html>`
	captchaNotNeeded = `{"isNeed":false,"isNeedActive":false}`
	minimalAuthHTML  = `<html><body><form id="pwdFromId" method="post" action="/authserver/login">
<input name="username" id="username" value=""><input name="passwordText" id="password" value="">
<input name="password" id="saltPassword" type="hidden"><input name="captcha" id="captcha" value="">
<div id="formErrorTip"><span id="showErrorTip"></span></div>
<input name="_eventId" id="_eventId" value="submit"><input name="cllt" id="cllt" value="userNameLogin">
<input name="dllt" id="dllt" value="generalLogin"><input name="lt" id="lt" value="">
<input id="pwdEncryptSalt" value="abcdefghijklmnop"><input name="execution" id="execution" value="e1s1">
</form></body></html>`
	rejectTip = "您提供的用户名或者密码有误"
)

type allowAll struct{}

func (allowAll) Allow(string, *url.URL) bool { return true }

// replay plays nap (ePortal + cas-sso), authserver and the connectivity check
// on httptest servers; the portal session moves as a real one would.
type replay struct {
	t                    *testing.T
	nap, auth, internet  *httptest.Server
	afterAuth            string // node once cas-sso accepts the ticket
	rejectCredentialPost bool   // authserver answers the POST with its 200 error page

	mu     sync.Mutex
	online bool
	node   string
	counts map[string]int // "METHOD /path" → requests
}

func newReplay(t *testing.T, online bool, afterAuth string, reject bool) *replay {
	t.Helper()
	rp := &replay{t: t, afterAuth: afterAuth, rejectCredentialPost: reject, online: online, node: "authenticate", counts: map[string]int{}}
	rp.nap = httptest.NewServer(http.HandlerFunc(rp.serveNap))
	rp.auth = httptest.NewServer(http.HandlerFunc(rp.serveAuth))
	rp.internet = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "Microsoft Connect Test")
	}))
	t.Cleanup(rp.nap.Close)
	t.Cleanup(rp.auth.Close)
	t.Cleanup(rp.internet.Close)
	return rp
}

// connector wires the production clients, as cmd/campusconnect does, to the replay origins.
func (rp *replay) connector() *Connector {
	hc := netguard.NewClient(allowAll{}, http.DefaultTransport, 7*time.Second)
	napURL, _ := url.Parse(rp.nap.URL)
	authURL, _ := url.Parse(rp.auth.URL)
	return &Connector{
		Portal:        portal.NewClient(hc, napURL),
		Authenticator: cas.NewAuthenticator(hc, napURL, authURL),
		Credentials:   func() (cas.Credentials, error) { return cas.Credentials{Username: "student", Password: "secret"}, nil },
		ServiceValue:  "CUCC",
		Probe:         portal.InternetProbe{HTTP: hc, URL: rp.internet.URL + "/connecttest.txt"},
		TargetSSID:    "NTVU-R",
		CurrentSSID:   func() (string, error) { return "NTVU-R", nil },
		Interval:      time.Millisecond,
	}
}

func (rp *replay) calls(key string) int {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.counts[key]
}

func (rp *replay) state() (online bool, node string) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.online, rp.node
}

// eportal decodes a JSON API body; another sessionId gets the portal's 会话不存在.
func (rp *replay) eportal(w http.ResponseWriter, r *http.Request) (map[string]string, bool) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["sessionId"] != replaySessionID {
		rp.t.Errorf("%s body = %v (%v)", r.URL.Path, body, err)
		io.WriteString(w, sessionGoneReply)
		return nil, false
	}
	return body, true
}

func (rp *replay) serveNap(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path
	rp.mu.Lock()
	rp.counts[key]++
	rp.mu.Unlock()
	q := r.URL.Query()
	switch key {
	case "GET /":
		http.Redirect(w, r, rp.nap.URL+"/eportal/redirect.jsp", http.StatusFound)
	case "GET /eportal/redirect.jsp":
		online, _ := rp.state()
		http.Redirect(w, r, rp.nap.URL+"/portal/portal-main?"+fmt.Sprintf(portalMainQuery, online), http.StatusFound)
	case "POST /eportal/network/userOnline":
		if _, ok := rp.eportal(w, r); !ok {
			return
		}
		if online, _ := rp.state(); online {
			io.WriteString(w, userOnlineTrue)
		} else {
			io.WriteString(w, userOnlineFalse)
		}
	case "POST /eportal/workFlow/getCurrentNode":
		body, ok := rp.eportal(w, r)
		if !ok {
			return
		}
		if body["flowKey"] != portal.FlowKey {
			rp.t.Errorf("getCurrentNode flowKey = %q", body["flowKey"])
		}
		_, node := rp.state()
		fmt.Fprintf(w, currentNodeReply, node)
	case "POST /eportal/network/serviceSelection":
		if _, ok := rp.eportal(w, r); ok {
			io.WriteString(w, serviceListReply)
		}
	case "POST /eportal/network/serviceLogin":
		body, ok := rp.eportal(w, r)
		if !ok {
			return
		}
		if body["service"] != "CUCC" {
			rp.t.Errorf("serviceLogin service = %q", body["service"])
		}
		rp.mu.Lock()
		rp.online, rp.node = true, "finish"
		rp.mu.Unlock()
		io.WriteString(w, serviceLoginReply)
	case "GET /cas-sso/login":
		if q.Get("ticket") != "" {
			// Ticket validated: the portal session is now authenticated.
			rp.mu.Lock()
			rp.node = rp.afterAuth
			rp.online = rp.afterAuth == "finish"
			rp.mu.Unlock()
			http.Redirect(w, r, rp.nap.URL+"/portal/entry/pc/authenticate", http.StatusFound)
			return
		}
		if q.Get("flowSessionId") != replaySessionID || q.Get("nodeMac") != replayUserMAC || q.Get("customPageId") != "4" {
			rp.t.Errorf("cas-sso login query = %v", q)
			http.Error(w, "bad portal context", http.StatusBadRequest)
			return
		}
		io.WriteString(w, adapterLoginHTML)
	case "GET /cas-sso/clientredirect":
		if q.Get("client_name") != "adapter" || q.Get("service") != rp.nap.URL+"/portal/entry/pc/authenticate" {
			rp.t.Errorf("clientredirect query = %v", q)
			http.Error(w, "bad client redirect", http.StatusBadRequest)
			return
		}
		service := rp.nap.URL + "/cas-sso/login?client_name=adapter&accept-language=zh-CN&delegatedclientid=TST-1"
		http.Redirect(w, r, rp.auth.URL+"/authserver/login?service="+url.QueryEscape(service), http.StatusFound)
	default:
		// Includes the portal SPA pages and any logout/offline endpoint.
		rp.t.Errorf("unexpected nap request %s", key)
		http.NotFound(w, r)
	}
}

func (rp *replay) serveAuth(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path
	rp.mu.Lock()
	rp.counts[key]++
	rp.mu.Unlock()
	switch key {
	case "GET /authserver/checkNeedCaptcha.htl":
		if r.URL.Query().Get("username") != "student" {
			rp.t.Errorf("checkNeedCaptcha username = %q", r.URL.Query().Get("username"))
		}
		io.WriteString(w, captchaNotNeeded)
	case "GET /authserver/login":
		io.WriteString(w, minimalAuthHTML)
	case "POST /authserver/login":
		if rp.rejectCredentialPost {
			io.WriteString(w, strings.Replace(minimalAuthHTML, `<span id="showErrorTip"></span>`, `<span id="showErrorTip">`+rejectTip+`</span>`, 1))
			return
		}
		http.Redirect(w, r, rp.nap.URL+"/cas-sso/login?client_name=adapter&ticket=ST-1-abc", http.StatusFound)
	default:
		rp.t.Errorf("unexpected authserver request %s", key)
		http.NotFound(w, r)
	}
}

func TestConnectReplaysPortalAndCASOverHTTP(t *testing.T) {
	for _, tc := range []struct {
		name          string
		online        bool
		afterAuth     string
		reject        bool
		want          Status
		posts, logins int // credential POSTs, serviceLogin calls
	}{
		{"already online", true, "finish", false, AlreadyOnline, 0, 0},
		{"authenticate then finish", false, "finish", false, Connected, 1, 0},
		{"authenticate then serviceSelection", false, "serviceSelection", false, Connected, 1, 1},
		{"authserver error page", false, "finish", true, AuthenticationRejected, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rp := newReplay(t, tc.online, tc.afterAuth, tc.reject)
			got := rp.connector().Connect(context.Background(), ModeFull)
			if got.Status != tc.want {
				t.Fatalf("result = %+v, want %s", got, tc.want)
			}
			if tc.reject && !strings.Contains(got.Message, rejectTip) {
				t.Errorf("message = %q, want the authserver tip", got.Message)
			}
			if n := rp.calls("POST /authserver/login"); n != tc.posts {
				t.Errorf("credential POSTs = %d, want %d", n, tc.posts)
			}
			if n := rp.calls("POST /eportal/network/serviceLogin"); n != tc.logins {
				t.Errorf("serviceLogin calls = %d, want %d", n, tc.logins)
			}
		})
	}
}
