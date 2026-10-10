package cas

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"campusconnect/internal/netguard"
)

// authFixture fakes nap (cas-sso) and authserver on two httptest servers.
type authFixture struct {
	nap, auth       *httptest.Server
	napURL, authURL *url.URL

	loginType string // current-login-type on /cas-sso/login
	captcha   string // checkNeedCaptcha.htl body
	// post answers the credential POST; nil redirects to a nap ticket callback.
	post func(w http.ResponseWriter, r *http.Request)
	// callback answers GET /cas-sso/login?ticket=...; nil redirects to the portal.
	callback func(w http.ResponseWriter, r *http.Request)

	mu       sync.Mutex
	requests []string
	posts    []url.Values
	postURL  string
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	f := &authFixture{loginType: "adapter", captcha: `{"isNeed":false,"isNeedActive":false}`}
	f.auth = httptest.NewServer(http.HandlerFunc(f.serveAuth))
	f.nap = httptest.NewServer(http.HandlerFunc(f.serveNap))
	t.Cleanup(f.nap.Close)
	t.Cleanup(f.auth.Close)
	f.napURL, _ = url.Parse(f.nap.URL)
	f.authURL, _ = url.Parse(f.auth.URL)
	return f
}

func (f *authFixture) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.Host+r.URL.Path)
}

func (f *authFixture) serveNap(w http.ResponseWriter, r *http.Request) {
	f.record(r)
	q := r.URL.Query()
	switch {
	case r.URL.Path == "/cas-sso/login" && q.Get("ticket") != "":
		if f.callback != nil {
			f.callback(w, r)
			return
		}
		http.Redirect(w, r, f.nap.URL+"/portal/entry/pc/authenticate", http.StatusFound)
	case r.URL.Path == "/cas-sso/login":
		if q.Get("flowSessionId") != "sid123" || q.Get("nodeMac") != "0123456789ab" || q.Get("customPageId") != "4" {
			http.Error(w, "bad portal context", http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, `<html><body><p id="current-login-type">%s</p><p id="login-error-code"></p></body></html>`, f.loginType)
	case r.URL.Path == "/cas-sso/clientredirect":
		if q.Get("client_name") != "adapter" || q.Get("service") != f.nap.URL+"/portal/entry/pc/authenticate" {
			http.Error(w, "bad client redirect", http.StatusBadRequest)
			return
		}
		service := f.nap.URL + "/cas-sso/login?client_name=adapter&accept-language=zh-CN&delegatedclientid=TST-1"
		http.Redirect(w, r, f.auth.URL+"/authserver/login?service="+url.QueryEscape(service), http.StatusFound)
	default:
		http.NotFound(w, r)
	}
}

func (f *authFixture) serveAuth(w http.ResponseWriter, r *http.Request) {
	f.record(r)
	switch {
	case r.URL.Path == "/authserver/checkNeedCaptcha.htl":
		if r.URL.Query().Get("username") != "student" {
			http.Error(w, "bad username", http.StatusBadRequest)
			return
		}
		io.WriteString(w, f.captcha)
	case r.URL.Path == "/authserver/login" && r.Method == http.MethodGet:
		io.WriteString(w, minimalAuthHTML)
	case r.URL.Path == "/authserver/login" && r.Method == http.MethodPost:
		r.ParseForm()
		f.mu.Lock()
		f.posts = append(f.posts, r.PostForm)
		f.postURL = r.URL.String()
		f.mu.Unlock()
		if f.post != nil {
			f.post(w, r)
			return
		}
		http.Redirect(w, r, f.nap.URL+"/cas-sso/login?client_name=adapter&ticket=ST-1-abc", http.StatusFound)
	default:
		http.NotFound(w, r)
	}
}

func (f *authFixture) login(t *testing.T) (Result, error) {
	t.Helper()
	hc := netguard.NewClient(allowAll{}, http.DefaultTransport, 7*time.Second)
	a := NewAuthenticator(hc, f.napURL, f.authURL)
	p := PortalContext{SessionID: "sid123", UserIP: "10.1.2.3", UserMAC: "0123456789ab", SSID: "Ruijie", NASIP: "10.18.0.1", CustomPageID: "4"}
	return a.Login(context.Background(), p, Credentials{Username: " student ", Password: "secret"})
}

func (f *authFixture) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.posts)
}

type allowAll struct{}

func (allowAll) Allow(string, *url.URL) bool { return true }

func wantKind(t *testing.T, err error, kind Kind) *Error {
	t.Helper()
	var ce *Error
	if !errors.As(err, &ce) || ce.Kind != kind {
		t.Fatalf("err = %v, want %s", err, kind)
	}
	return ce
}

func TestLoginSubmitsEncryptedPasswordOnceAndFollowsCallback(t *testing.T) {
	f := newAuthFixture(t)
	res, err := f.login(t)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.ReturnURL == nil || res.ReturnURL.Path != "/portal/entry/pc/authenticate" {
		t.Fatalf("ReturnURL = %v", res.ReturnURL)
	}
	if f.postCount() != 1 {
		t.Fatalf("credential POSTs = %d, want 1", f.postCount())
	}
	form := f.posts[0]
	for k, want := range map[string]string{"username": "student", "captcha": "", "_eventId": "submit", "cllt": "userNameLogin", "dllt": "generalLogin", "lt": "", "execution": "e1s1"} {
		if got, ok := form[k]; !ok || got[0] != want {
			t.Errorf("form[%s] = %v, want %q", k, got, want)
		}
	}
	if _, ok := form["passwordText"]; ok {
		t.Error("plaintext passwordText field was sent")
	}
	if strings.Contains(form.Get("password"), "secret") {
		t.Error("password sent in clear")
	}
	if !strings.Contains(f.postURL, "service=") {
		t.Errorf("POST URL %q lost the service parameter", f.postURL)
	}
	// The portal SPA itself is never fetched.
	for _, r := range f.requests {
		if strings.Contains(r, "/portal/") {
			t.Errorf("fetched portal page: %s", r)
		}
	}
}

func TestLoginRequiresAdapterLoginType(t *testing.T) {
	f := newAuthFixture(t)
	f.loginType = "UsernamePassword"
	_, err := f.login(t)
	wantKind(t, err, PortalChanged)
	if f.postCount() != 0 {
		t.Fatal("credentials posted to a non-adapter flow")
	}
}

func TestLoginStopsBeforePostWhenCaptchaRequired(t *testing.T) {
	f := newAuthFixture(t)
	f.captcha = `{"isNeed":true}`
	_, err := f.login(t)
	wantKind(t, err, CaptchaRequired)
	if f.postCount() != 0 {
		t.Fatalf("credential POSTs = %d, want 0", f.postCount())
	}
}

func TestLoginStopsWhenAccountNeedsActivation(t *testing.T) {
	f := newAuthFixture(t)
	f.captcha = `{"isNeed":false,"isNeedActive":true}`
	_, err := f.login(t)
	wantKind(t, err, NeedsAttention)
	if f.postCount() != 0 {
		t.Fatalf("credential POSTs = %d, want 0", f.postCount())
	}
}

func TestLoginRejectedPasswordReturnsErrorTip(t *testing.T) {
	f := newAuthFixture(t)
	f.post = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, strings.Replace(minimalAuthHTML, `<span id="showErrorTip"></span>`, `<span id="showErrorTip">您提供的用户名或者密码有误</span>`, 1))
	}
	_, err := f.login(t)
	ce := wantKind(t, err, AuthenticationRejected)
	if ce.Detail != "您提供的用户名或者密码有误" {
		t.Fatalf("detail = %q", ce.Detail)
	}
	if f.postCount() != 1 {
		t.Fatalf("credential POSTs = %d, want exactly 1", f.postCount())
	}
}

func TestLoginRejectsForeignCallbackHost(t *testing.T) {
	f := newAuthFixture(t)
	f.post = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/cas-sso/login?ticket=ST-1", http.StatusFound)
	}
	_, err := f.login(t)
	wantKind(t, err, PortalChanged)
	for _, r := range f.requests {
		if strings.Contains(r, "evil.example") {
			t.Fatalf("followed foreign callback: %s", r)
		}
	}
}

func TestLoginAuthserverInterstitialNeedsAttention(t *testing.T) {
	f := newAuthFixture(t)
	f.post = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, f.auth.URL+"/authserver/reAuthCheck/changePassword", http.StatusFound)
	}
	_, err := f.login(t)
	wantKind(t, err, NeedsAttention)
}

func TestLoginCallbackErrorCodes(t *testing.T) {
	for code, kind := range map[string]Kind{
		"1030027": AuthenticationRejected,
		"1410041": AuthenticationRejected,
		"1030028": AccountLocked,
		"1330001": AccountRisk,
		"1320007": CaptchaRequired,
		"9999999": NeedsAttention,
	} {
		t.Run(code, func(t *testing.T) {
			f := newAuthFixture(t)
			f.callback = func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `<p id="current-login-type">adapter</p><p id="login-error-code">%s</p>`, code)
			}
			_, err := f.login(t)
			wantKind(t, err, kind)
		})
	}
}

func TestLoginCallbackBindPageNeedsAttention(t *testing.T) {
	f := newAuthFixture(t)
	f.callback = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, f.nap.URL+"/cas-sso/client/bind/fail?clientname=adapter", http.StatusFound)
	}
	_, err := f.login(t)
	wantKind(t, err, NeedsAttention)
}

func TestLoginCallbackStopsAfterThreeHops(t *testing.T) {
	f := newAuthFixture(t)
	hops := 0
	f.callback = func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, f.nap.URL+"/cas-sso/login?ticket=ST-loop", http.StatusFound)
	}
	_, err := f.login(t)
	wantKind(t, err, PortalChanged)
	if hops != 3 {
		t.Fatalf("callback hops = %d, want 3", hops)
	}
}

const minimalAuthHTML = `<html><body><form id="pwdFromId" method="post" action="/authserver/login">
<input name="username" id="username" value=""><input name="passwordText" id="password" value="">
<input name="password" id="saltPassword" type="hidden"><input name="captcha" id="captcha" value="">
<div id="formErrorTip"><span id="showErrorTip"></span></div>
<input name="_eventId" id="_eventId" value="submit"><input name="cllt" id="cllt" value="userNameLogin">
<input name="dllt" id="dllt" value="generalLogin"><input name="lt" id="lt" value="">
<input id="pwdEncryptSalt" value="abcdefghijklmnop"><input name="execution" id="execution" value="e1s1">
</form></body></html>`
