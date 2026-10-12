package flow

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"campusconnect/internal/cas"
	"campusconnect/internal/netguard"
	"campusconnect/internal/portal"
)

// fakePortal scripts the portal: nodes are returned in order (last repeats),
// online answers userOnline in order (last repeats).
type fakePortal struct {
	discoverErr  error
	online       []bool
	onlineErr    error
	nodes        []string
	services     []portal.Service
	serviceErr   error
	loginErr     error
	onlineCalls  int
	nodeCalls    int
	serviceLogin []string
}

func (f *fakePortal) Discover(context.Context) (portal.Session, error) {
	if f.discoverErr != nil {
		return portal.Session{}, f.discoverErr
	}
	return portal.Session{ID: "sid", UserIP: "10.0.0.2", UserMAC: "aabbccddeeff", SSID: "Ruijie", NASIP: "10.0.0.1", CustomPageID: "4"}, nil
}

func (f *fakePortal) UserOnline(context.Context, string) (bool, error) {
	i := min(f.onlineCalls, len(f.online)-1)
	f.onlineCalls++
	return f.online[i], f.onlineErr
}

func (f *fakePortal) OnlineUserInfo(context.Context, string) (portal.OnlineInfo, bool, error) {
	return portal.OnlineInfo{}, false, nil
}

func (f *fakePortal) CurrentNode(_ context.Context, sid, flowKey string) (portal.Node, error) {
	if sid != "sid" || flowKey != portal.FlowKey {
		return portal.Node{}, fmt.Errorf("bad args %q %q", sid, flowKey)
	}
	i := min(f.nodeCalls, len(f.nodes)-1)
	f.nodeCalls++
	return portal.Node{Path: f.nodes[i]}, nil
}

func (f *fakePortal) Services(context.Context, string) ([]portal.Service, error) {
	return f.services, f.serviceErr
}

func (f *fakePortal) ServiceLogin(_ context.Context, _ string, service string) error {
	f.serviceLogin = append(f.serviceLogin, service)
	return f.loginErr
}

type fakeAuth struct {
	err    error
	calls  int
	gotCtx cas.PortalContext
	onCall func()
}

func (a *fakeAuth) Login(_ context.Context, p cas.PortalContext, c cas.Credentials) (cas.Result, error) {
	a.calls++
	a.gotCtx = p
	if a.onCall != nil {
		a.onCall()
	}
	return cas.Result{}, a.err
}

type probe bool

func (p probe) Internet(context.Context) bool { return bool(p) }

var campusServices = []portal.Service{{Label: "校园网", Value: "PUBLIC"}, {Label: "中国联通", Value: "CUCC"}}

func newConnector(p *fakePortal, a *fakeAuth, internet bool) *Connector {
	return &Connector{
		Portal:        p,
		Authenticator: a,
		Credentials:   func() (cas.Credentials, error) { return cas.Credentials{Username: "student", Password: "pw"}, nil },
		ServiceValue:  "CUCC",
		Probe:         probe(internet),
		TargetSSID:    "NTVU-R",
		CurrentSSID:   func() (string, error) { return "NTVU-R", nil },
		Interval:      time.Microsecond,
	}
}

func TestConnectAlreadyOnlineWithInternetFailureDoesNotAuthenticate(t *testing.T) {
	p := &fakePortal{online: []bool{true}, nodes: []string{"authenticate"}}
	a := &fakeAuth{}
	got := newConnector(p, a, false).Connect(context.Background(), ModeFull)
	if got.Status != OnlineButInternetUnavailable {
		t.Fatalf("status = %s, want OnlineButInternetUnavailable", got.Status)
	}
	if a.calls != 0 || p.nodeCalls != 0 || len(p.serviceLogin) != 0 {
		t.Fatalf("wrote while online: auth=%d nodes=%d serviceLogin=%v", a.calls, p.nodeCalls, p.serviceLogin)
	}
}

func TestConnectAlreadyOnline(t *testing.T) {
	p := &fakePortal{online: []bool{true}}
	if got := newConnector(p, &fakeAuth{}, true).Connect(context.Background(), ModeFull); got.Status != AlreadyOnline {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestConnectAuthenticatesThenSelectsConfiguredService(t *testing.T) {
	p := &fakePortal{online: []bool{false, false, true}, services: campusServices}
	a := &fakeAuth{}
	// authenticate → (after CAS) authenticate, serviceSelection
	p.nodes = []string{"authenticate", "authenticate", "serviceSelection"}
	got := newConnector(p, a, true).Connect(context.Background(), ModeFull)
	if got.Status != Connected {
		t.Fatalf("status = %s (%s), want Connected", got.Status, got.Message)
	}
	if a.calls != 1 {
		t.Fatalf("CAS logins = %d, want 1", a.calls)
	}
	want := cas.PortalContext{SessionID: "sid", UserIP: "10.0.0.2", UserMAC: "aabbccddeeff", SSID: "Ruijie", NASIP: "10.0.0.1", CustomPageID: "4"}
	if a.gotCtx != want {
		t.Fatalf("portal context = %+v", a.gotCtx)
	}
	if len(p.serviceLogin) != 1 || p.serviceLogin[0] != "CUCC" {
		t.Fatalf("serviceLogin calls = %v, want [CUCC]", p.serviceLogin)
	}
}

func TestConnectFinishNodeConfirmsOnline(t *testing.T) {
	p := &fakePortal{online: []bool{false, true}, nodes: []string{"finish"}}
	got := newConnector(p, &fakeAuth{}, true).Connect(context.Background(), ModeFull)
	if got.Status != Connected {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestConnectStopsAtUnknownNode(t *testing.T) {
	p := &fakePortal{online: []bool{false}, nodes: []string{"bindPhone"}, services: campusServices}
	a := &fakeAuth{}
	got := newConnector(p, a, true).Connect(context.Background(), ModeFull)
	if got.Status != NeedsAttention || !strings.Contains(got.Message, "bindPhone") {
		t.Fatalf("result = %+v, want NeedsAttention naming the node", got)
	}
	if a.calls != 0 || len(p.serviceLogin) != 0 {
		t.Fatalf("wrote at unknown node: auth=%d serviceLogin=%v", a.calls, p.serviceLogin)
	}
}

func TestConnectHonorsNodeAndPollBounds(t *testing.T) {
	t.Run("still authenticate after CAS polls 10 times", func(t *testing.T) {
		p := &fakePortal{online: []bool{false}, nodes: []string{"authenticate"}}
		a := &fakeAuth{}
		got := newConnector(p, a, true).Connect(context.Background(), ModeFull)
		if got.Status != NeedsAttention {
			t.Fatalf("status = %s", got.Status)
		}
		if a.calls != 1 {
			t.Fatalf("CAS logins = %d, want 1", a.calls)
		}
		if p.nodeCalls != 1+10 {
			t.Fatalf("node calls = %d, want 11", p.nodeCalls)
		}
	})
	t.Run("online confirmation stops after 15 checks", func(t *testing.T) {
		p := &fakePortal{online: []bool{false}, nodes: []string{"finish"}}
		got := newConnector(p, &fakeAuth{}, true).Connect(context.Background(), ModeFull)
		if got.Status != TimedOut {
			t.Fatalf("status = %s", got.Status)
		}
		if p.onlineCalls != 1+15 {
			t.Fatalf("userOnline calls = %d, want 16", p.onlineCalls)
		}
	})
	t.Run("serviceSelection submits serviceLogin once then times out confirming online", func(t *testing.T) {
		// Confirmation polling after serviceLogin never resubmits it.
		p := &fakePortal{online: []bool{false}, nodes: []string{"serviceSelection"}, services: campusServices}
		p.online = []bool{false, false}
		c := newConnector(p, &fakeAuth{}, true)
		got := c.Connect(context.Background(), ModeFull)
		if len(p.serviceLogin) != 1 {
			t.Fatalf("serviceLogin calls = %d, want 1", len(p.serviceLogin))
		}
		if got.Status != TimedOut {
			t.Fatalf("status = %s", got.Status)
		}
	})
}

func TestConnectCheckOnlyNeverWrites(t *testing.T) {
	p := &fakePortal{online: []bool{false}, nodes: []string{"authenticate"}, services: campusServices}
	a := &fakeAuth{}
	got := newConnector(p, a, true).Connect(context.Background(), ModeCheckOnly)
	if got.Status != Deferred {
		t.Fatalf("status = %s", got.Status)
	}
	if a.calls != 0 || len(p.serviceLogin) != 0 || p.nodeCalls != 0 {
		t.Fatalf("check-only wrote: auth=%d serviceLogin=%v nodes=%d", a.calls, p.serviceLogin, p.nodeCalls)
	}
}

func TestConnectSSIDGate(t *testing.T) {
	p := &fakePortal{online: []bool{true}}
	c := newConnector(p, &fakeAuth{}, true)
	c.CurrentSSID = func() (string, error) { return "Home-WiFi", nil }
	if got := c.Connect(context.Background(), ModeFull); got.Status != NotTargetNetwork {
		t.Fatalf("status = %s", got.Status)
	}
	if p.onlineCalls != 0 {
		t.Fatal("contacted portal on a foreign network")
	}

	// SSID unreadable: continue; a failed Discover is NetworkNotReady.
	p = &fakePortal{discoverErr: &portal.Error{Kind: portal.KindUnavailable, Op: "discover"}}
	c = newConnector(p, &fakeAuth{}, true)
	c.CurrentSSID = func() (string, error) { return "", errors.New("location denied") }
	if got := c.Connect(context.Background(), ModeFull); got.Status != NetworkNotReady {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestConnectMissingCredentialsIsConfigurationRequired(t *testing.T) {
	p := &fakePortal{online: []bool{false}, nodes: []string{"authenticate"}}
	a := &fakeAuth{}
	c := newConnector(p, a, true)
	c.Credentials = func() (cas.Credentials, error) { return cas.Credentials{}, nil }
	if got := c.Connect(context.Background(), ModeFull); got.Status != ConfigurationRequired {
		t.Fatalf("status = %s", got.Status)
	}
	if a.calls != 0 {
		t.Fatal("logged in without credentials")
	}
}

func TestConnectServiceNotFound(t *testing.T) {
	p := &fakePortal{online: []bool{false}, nodes: []string{"serviceSelection"}, services: campusServices}
	c := newConnector(p, &fakeAuth{}, true)
	c.ServiceValue = "CTCC"
	if got := c.Connect(context.Background(), ModeFull); got.Status != ServiceNotFound {
		t.Fatalf("status = %s", got.Status)
	}
	if len(p.serviceLogin) != 0 {
		t.Fatal("serviceLogin called with unknown service")
	}
}

func TestConnectClassifiesErrors(t *testing.T) {
	blocked := fmt.Errorf("wrap: %w", netguard.ErrBlocked)
	for name, tc := range map[string]struct {
		discover error
		auth     error
		want     Status
	}{
		"portal changed":     {discover: &portal.Error{Kind: portal.KindChanged, Op: "discover"}, want: PortalChanged},
		"portal unavailable": {discover: &portal.Error{Kind: portal.KindUnavailable, Op: "discover"}, want: PortalUnavailable},
		"blocked request":    {discover: &portal.Error{Kind: portal.KindUnavailable, Op: "discover", Err: blocked}, want: InternalError},
		"cas rejected":       {auth: &cas.Error{Kind: cas.AuthenticationRejected, Step: "login"}, want: AuthenticationRejected},
		"cas locked":         {auth: &cas.Error{Kind: cas.AccountLocked}, want: AccountLocked},
		"cas captcha":        {auth: &cas.Error{Kind: cas.CaptchaRequired}, want: CaptchaRequired},
		"cas changed":        {auth: &cas.Error{Kind: cas.PortalChanged}, want: PortalChanged},
	} {
		t.Run(name, func(t *testing.T) {
			p := &fakePortal{discoverErr: tc.discover, online: []bool{false}, nodes: []string{"authenticate"}}
			got := newConnector(p, &fakeAuth{err: tc.auth}, true).Connect(context.Background(), ModeFull)
			if got.Status != tc.want {
				t.Fatalf("status = %s (%s), want %s", got.Status, got.Message, tc.want)
			}
		})
	}
}

func TestConnectMessageOmitsRequestURL(t *testing.T) {
	inner := &url.Error{Op: "Get", URL: "https://nap.ntvu.edu.cn/eportal/adaptor/getOnlineUserInfo?sessionId=secret123", Err: errors.New("connection reset")}
	p := &fakePortal{discoverErr: &portal.Error{Kind: portal.KindUnavailable, Op: "discover", Err: inner}}
	got := newConnector(p, &fakeAuth{}, true).Connect(context.Background(), ModeFull)
	if strings.Contains(got.Message, "secret123") || !strings.Contains(got.Message, "connection reset") {
		t.Fatalf("message = %q", got.Message)
	}
}

func TestConnectOverallDeadlineIsTimedOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &fakePortal{online: []bool{false}, nodes: []string{"finish"}}
	a := &fakeAuth{}
	c := newConnector(p, a, true)
	c.Probe = probe(true)
	cancel()
	if got := c.Connect(ctx, ModeFull); got.Status != TimedOut {
		t.Fatalf("status = %s", got.Status)
	}
}
