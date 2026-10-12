// Package cas walks the portal's delegated sign-in: cas-sso adapter on
// nap.ntvu.edu.cn hands off to authserver.ntvu.edu.cn (spec §2.4).
package cas

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"campusconnect/internal/netguard"
)

// Production origins.
const (
	NapBaseURL  = "https://nap.ntvu.edu.cn"
	AuthBaseURL = "https://authserver.ntvu.edu.cn"
)

const (
	maxBody          = 1 << 20
	maxCallbackHops  = 3
	portalReturnPath = "/portal/entry/pc/authenticate"
)

// PortalContext is the portal session the sign-in is bound to.
type PortalContext struct {
	SessionID, UserIP, UserMAC, SSID, NASIP, CustomPageID string
}

// Credentials are the campus account; Password is plaintext only in memory.
type Credentials struct {
	Username, Password string
}

// Result is where cas-sso finally sent the browser back to the portal.
type Result struct {
	ReturnURL *url.URL
}

// Authenticator performs one delegated sign-in per Login call.
type Authenticator struct {
	http      *netguard.Client
	nap, auth *url.URL
	random    io.Reader // nil means crypto/rand
}

// NewAuthenticator binds the flow to the given origins (scheme://host).
func NewAuthenticator(httpClient *netguard.Client, napBase, authBase *url.URL) *Authenticator {
	return &Authenticator{http: httpClient, nap: origin(napBase), auth: origin(authBase)}
}

func origin(u *url.URL) *url.URL {
	return &url.URL{Scheme: u.Scheme, Host: u.Host}
}

func (a *Authenticator) at(base *url.URL, path string, q url.Values) *url.URL {
	u := *base
	u.Path = path
	u.RawQuery = q.Encode()
	return &u
}

type reply struct {
	status   int
	location *url.URL
	body     []byte
}

// do sends one request without following redirects and reads a bounded body.
func (a *Authenticator) do(ctx context.Context, step, method string, u *url.URL, form url.Values) (reply, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return reply{}, classified(step, PortalChanged, "", err)
	}
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return reply{}, classified(step, PortalUnavailable, "", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return reply{}, classified(step, PortalUnavailable, "", err)
	}
	r := reply{status: resp.StatusCode, body: b}
	if isRedirect(resp.StatusCode) {
		loc, err := resp.Location()
		if err != nil {
			return reply{}, classified(step, PortalChanged, "redirect without Location", err)
		}
		r.location = loc
	} else if resp.StatusCode >= 500 {
		return reply{}, classified(step, PortalUnavailable, "HTTP "+strconv.Itoa(resp.StatusCode), nil)
	}
	return r, nil
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

func sameOrigin(u, base *url.URL) bool {
	return u.Scheme == base.Scheme && u.Host == base.Host && u.User == nil
}

func millis() string {
	return strconv.FormatInt(time.Now().UnixMilli(), 10)
}

// Login runs spec §2.4 steps 1–7. It POSTs credentials at most once and only
// to the authserver login URL; it never follows a redirect off the two origins.
func (a *Authenticator) Login(ctx context.Context, p PortalContext, c Credentials) (Result, error) {
	username := strings.TrimSpace(c.Username)
	if username == "" || c.Password == "" {
		return Result{}, classified("credentials", NeedsAttention, "username or password missing", nil)
	}
	if err := a.openAdapter(ctx, p); err != nil {
		return Result{}, err
	}
	loginURL, err := a.clientRedirect(ctx)
	if err != nil {
		return Result{}, err
	}
	form, err := a.loadForm(ctx, loginURL)
	if err != nil {
		return Result{}, err
	}
	if err := a.precheck(ctx, username); err != nil {
		return Result{}, err
	}
	callback, err := a.submit(ctx, loginURL, form, username, c.Password)
	if err != nil {
		return Result{}, err
	}
	return a.followCallback(ctx, callback)
}

// Step 1: the cas-sso page must be in adapter mode.
func (a *Authenticator) openAdapter(ctx context.Context, p PortalContext) error {
	const step = "cas-sso login"
	q := url.Values{
		"flowSessionId": {p.SessionID},
		"customPageId":  {p.CustomPageID},
		"preview":       {"false"},
		"appType":       {"normal"},
		"language":      {"zh-CN"},
		"timer":         {millis()},
		"nasIp":         {p.NASIP},
		"userIp":        {p.UserIP},
		"ssid":          {p.SSID},
		"nodeMac":       {p.UserMAC},
	}
	r, err := a.do(ctx, step, http.MethodGet, a.at(a.nap, "/cas-sso/login", q), nil)
	if err != nil {
		return err
	}
	if r.status != http.StatusOK {
		return classified(step, PortalChanged, "HTTP "+strconv.Itoa(r.status), nil)
	}
	loginType, err := parseElementText(bytes.NewReader(r.body), "current-login-type")
	if err != nil || loginType != "adapter" {
		return classified(step, PortalChanged, "login type is not adapter", err)
	}
	return nil
}

// Step 2: clientredirect must hand off to authserver with a cas-sso service.
func (a *Authenticator) clientRedirect(ctx context.Context) (*url.URL, error) {
	const step = "clientredirect"
	q := url.Values{
		"client_name":     {"adapter"},
		"accept-language": {"zh-CN"},
		"service":         {a.at(a.nap, portalReturnPath, nil).String()},
	}
	r, err := a.do(ctx, step, http.MethodGet, a.at(a.nap, "/cas-sso/clientredirect", q), nil)
	if err != nil {
		return nil, err
	}
	loc := r.location
	if loc == nil || !sameOrigin(loc, a.auth) || loc.Path != "/authserver/login" {
		return nil, classified(step, PortalChanged, "unexpected authserver handoff", nil)
	}
	service, err := url.Parse(loc.Query().Get("service"))
	if err != nil || !sameOrigin(service, a.nap) || service.Path != "/cas-sso/login" {
		return nil, classified(step, PortalChanged, "authserver service is not cas-sso", err)
	}
	return loc, nil
}

// Step 3: the password form, scoped to #pwdFromId.
func (a *Authenticator) loadForm(ctx context.Context, loginURL *url.URL) (loginForm, error) {
	const step = "authserver form"
	r, err := a.do(ctx, step, http.MethodGet, loginURL, nil)
	if err != nil {
		return loginForm{}, err
	}
	if r.status != http.StatusOK {
		return loginForm{}, classified(step, PortalChanged, "HTTP "+strconv.Itoa(r.status), nil)
	}
	form, err := parseLoginForm(bytes.NewReader(r.body))
	if err != nil {
		return loginForm{}, classified(step, PortalChanged, "", err)
	}
	return form, nil
}

// Step 4: never submit when authserver wants a captcha or activation.
func (a *Authenticator) precheck(ctx context.Context, username string) error {
	const step = "checkNeedCaptcha"
	q := url.Values{"username": {username}, "_": {millis()}}
	r, err := a.do(ctx, step, http.MethodGet, a.at(a.auth, "/authserver/checkNeedCaptcha.htl", q), nil)
	if err != nil {
		return err
	}
	var v struct {
		IsNeed       *bool `json:"isNeed"`
		IsNeedActive bool  `json:"isNeedActive"`
	}
	if r.status != http.StatusOK || json.Unmarshal(r.body, &v) != nil || v.IsNeed == nil {
		return classified(step, PortalChanged, "unexpected captcha check response", nil)
	}
	if *v.IsNeed {
		return classified(step, CaptchaRequired, "", nil)
	}
	if v.IsNeedActive {
		return classified(step, NeedsAttention, "account needs activation", nil)
	}
	return nil
}

// Steps 5–6: the single credential POST.
func (a *Authenticator) submit(ctx context.Context, loginURL *url.URL, f loginForm, username, password string) (*url.URL, error) {
	const step = "authserver login"
	encrypted, err := encryptPassword(password, f.Salt, a.random)
	if err != nil {
		return nil, classified(step, PortalChanged, "", err)
	}
	form := url.Values{
		"username":  {username},
		"password":  {encrypted},
		"captcha":   {""},
		"_eventId":  {f.EventID},
		"cllt":      {f.CLLT},
		"dllt":      {f.DLLT},
		"lt":        {f.LT},
		"execution": {f.Execution},
	}
	r, err := a.do(ctx, step, http.MethodPost, loginURL, form)
	if err != nil {
		return nil, err
	}
	loc := r.location
	switch {
	case loc == nil:
		if r.status != http.StatusOK {
			return nil, classified(step, PortalChanged, "HTTP "+strconv.Itoa(r.status), nil)
		}
		tip, _ := parseErrorTip(bytes.NewReader(r.body))
		return nil, classified(step, AuthenticationRejected, tip, nil)
	case sameOrigin(loc, a.nap) && loc.Path == "/cas-sso/login" && loc.Query().Get("ticket") != "":
		return loc, nil
	case sameOrigin(loc, a.nap) || sameOrigin(loc, a.auth):
		return nil, classified(step, NeedsAttention, "authserver redirected to "+loc.Path, nil)
	default:
		return nil, classified(step, PortalChanged, "redirect leaves campus hosts: "+loc.Host, nil)
	}
}

// Step 7: follow the ticket callback within /cas-sso/login, at most 3 hops.
func (a *Authenticator) followCallback(ctx context.Context, current *url.URL) (Result, error) {
	const step = "cas-sso callback"
	for range maxCallbackHops {
		r, err := a.do(ctx, step, http.MethodGet, current, nil)
		if err != nil {
			return Result{}, err
		}
		loc := r.location
		if loc == nil {
			code, _ := parseElementText(bytes.NewReader(r.body), "login-error-code")
			return Result{}, classified(step, errorCodeKind(code), "login-error-code "+code, nil)
		}
		switch {
		case !sameOrigin(loc, a.nap):
			return Result{}, classified(step, PortalChanged, "redirect leaves portal host: "+loc.Host, nil)
		case strings.HasPrefix(loc.Path, "/cas-sso/client/bind/"):
			return Result{}, classified(step, NeedsAttention, "third-party account not bound", nil)
		case loc.Path == "/cas-sso/login":
			current = loc
		case strings.HasPrefix(loc.Path, "/cas-sso/") || loc.Path == "/cas-sso":
			return Result{}, classified(step, NeedsAttention, "unexpected cas-sso path "+loc.Path, nil)
		default:
			return Result{ReturnURL: loc}, nil
		}
	}
	return Result{}, classified(step, PortalChanged, "too many cas-sso redirects", nil)
}

func errorCodeKind(code string) Kind {
	switch code {
	case "1030027", "1030031", "1410040", "1410041":
		return AuthenticationRejected
	case "1030028":
		return AccountLocked
	case "1330001":
		return AccountRisk
	case "1320007", "1320010":
		return CaptchaRequired
	}
	return NeedsAttention
}
