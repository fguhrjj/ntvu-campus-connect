package flow

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"campusconnect/internal/cas"
	"campusconnect/internal/netguard"
	"campusconnect/internal/portal"
)

// Bounds from spec §4.
const (
	maxNodeRounds     = 4
	maxAuthPolls      = 10
	maxOnlineChecks   = 15
	defaultTimeout    = 60 * time.Second
	defaultPollPeriod = time.Second
)

// Connector runs one connection attempt. Build a fresh one (with a fresh
// HTTP client) per attempt so cookies never outlive it.
type Connector struct {
	Portal        Portal
	Authenticator Authenticator
	// Credentials is called only when the portal asks for sign-in, so the
	// password is decrypted just before it is submitted.
	Credentials  func() (cas.Credentials, error)
	ServiceValue string
	Probe        Probe
	// TargetSSID gates the attempt when CurrentSSID reads successfully.
	TargetSSID  string
	CurrentSSID func() (string, error)
	// Timeout bounds the whole attempt (default 60 s); Interval spaces polls (default 1 s).
	Timeout  time.Duration
	Interval time.Duration
	// Logf, when set, receives progress lines without secrets.
	Logf func(format string, args ...any)
}

// Connect performs spec §4 once and never calls a logout/offline endpoint.
func (c *Connector) Connect(ctx context.Context, mode Mode) Result {
	ctx, cancel := context.WithTimeout(ctx, or(c.Timeout, defaultTimeout))
	defer cancel()
	if ctx.Err() != nil {
		return Result{Status: TimedOut, Message: "连接已取消或超时"}
	}

	ssidKnown := false
	if c.CurrentSSID != nil {
		ssid, err := c.CurrentSSID()
		switch {
		case err != nil:
			c.logf("读取 SSID 失败，继续探测门户：%v", err)
		case ssid != c.TargetSSID:
			return Result{Status: NotTargetNetwork, Message: "当前 WLAN：" + ssid}
		default:
			ssidKnown = true
		}
	}

	sess, err := c.Portal.Discover(ctx)
	if err != nil {
		r := classify(ctx, err)
		if !ssidKnown && r.Status != InternalError {
			r.Status = NetworkNotReady
		}
		return r
	}
	online, err := c.Portal.UserOnline(ctx, sess.ID)
	if err != nil {
		return classify(ctx, err)
	}
	if online {
		return c.onlineResult(ctx, AlreadyOnline)
	}
	if mode == ModeCheckOnly {
		return Result{Status: Deferred, Message: "门户离线，退避期间不提交登录"}
	}
	return c.login(ctx, sess)
}

func (c *Connector) login(ctx context.Context, sess portal.Session) Result {
	node, err := c.Portal.CurrentNode(ctx, sess.ID, portal.FlowKey)
	if err != nil {
		return classify(ctx, err)
	}
	authenticated := false
	for range maxNodeRounds {
		c.logf("门户节点：%s", node.Path)
		switch node.Path {
		case "authenticate":
			if authenticated {
				return Result{Status: NeedsAttention, Message: "统一身份认证后门户仍要求认证"}
			}
			authenticated = true
			if r, ok := c.authenticate(ctx, sess); !ok {
				return r
			}
			if node, err = c.pollAfterAuth(ctx, sess.ID); err != nil {
				return classify(ctx, err)
			}
			if node.Path == "authenticate" {
				return Result{Status: NeedsAttention, Message: "统一身份认证后门户仍停留在认证页"}
			}
		case "serviceSelection":
			if r, ok := c.selectService(ctx, sess.ID); !ok {
				return r
			}
			return c.confirmOnline(ctx, sess.ID)
		case "finish", "loginSuccess":
			return c.confirmOnline(ctx, sess.ID)
		default:
			return Result{Status: NeedsAttention, Message: "未支持的门户节点：" + node.Path}
		}
	}
	return Result{Status: NeedsAttention, Message: "门户流程节点过多"}
}

func (c *Connector) authenticate(ctx context.Context, sess portal.Session) (Result, bool) {
	if c.Credentials == nil {
		return Result{Status: ConfigurationRequired, Message: "未设置学号/密码"}, false
	}
	creds, err := c.Credentials()
	if err != nil || strings.TrimSpace(creds.Username) == "" || creds.Password == "" {
		msg := "未设置学号/密码"
		if err != nil {
			msg = "无法读取已保存的密码：" + sanitize(err)
		}
		return Result{Status: ConfigurationRequired, Message: msg}, false
	}
	p := cas.PortalContext{SessionID: sess.ID, UserIP: sess.UserIP, UserMAC: sess.UserMAC, SSID: sess.SSID, NASIP: sess.NASIP, CustomPageID: sess.CustomPageID}
	if _, err := c.Authenticator.Login(ctx, p, creds); err != nil {
		return classify(ctx, err), false
	}
	return Result{}, true
}

// pollAfterAuth waits for the node to leave authenticate, at most 10 polls.
func (c *Connector) pollAfterAuth(ctx context.Context, sid string) (portal.Node, error) {
	node := portal.Node{Path: "authenticate"}
	for range maxAuthPolls {
		if err := c.wait(ctx); err != nil {
			return node, err
		}
		n, err := c.Portal.CurrentNode(ctx, sid, portal.FlowKey)
		if err != nil {
			return node, err
		}
		if node = n; node.Path != "authenticate" {
			return node, nil
		}
	}
	return node, nil
}

// selectService submits the configured operator once.
func (c *Connector) selectService(ctx context.Context, sid string) (Result, bool) {
	services, err := c.Portal.Services(ctx, sid)
	if err != nil {
		return classify(ctx, err), false
	}
	found := false
	for _, s := range services {
		if s.Value == c.ServiceValue {
			found = true
			break
		}
	}
	if !found || c.ServiceValue == "" {
		return Result{Status: ServiceNotFound, Message: "运营商不在门户列表中：" + c.ServiceValue}, false
	}
	if err := c.Portal.ServiceLogin(ctx, sid, c.ServiceValue); err != nil {
		return classify(ctx, err), false
	}
	return Result{}, true
}

// confirmOnline checks the portal state at most 15 times.
func (c *Connector) confirmOnline(ctx context.Context, sid string) Result {
	for i := range maxOnlineChecks {
		if i > 0 {
			if err := c.wait(ctx); err != nil {
				break
			}
		}
		if c.isOnline(ctx, sid) {
			return c.onlineResult(ctx, Connected)
		}
	}
	return Result{Status: TimedOut, Message: "提交后未确认在线"}
}

// isOnline treats query failures as "not yet" (spec §2.5): the fallback
// query never leads to a logout branch.
func (c *Connector) isOnline(ctx context.Context, sid string) bool {
	online, err := c.Portal.UserOnline(ctx, sid)
	if err == nil {
		return online
	}
	_, online, err = c.Portal.OnlineUserInfo(ctx, sid)
	return err == nil && online
}

func (c *Connector) onlineResult(ctx context.Context, status Status) Result {
	if c.Probe != nil && !c.Probe.Internet(ctx) {
		return Result{Status: OnlineButInternetUnavailable, Message: "门户在线但外网探测失败"}
	}
	return Result{Status: status}
}

func (c *Connector) wait(ctx context.Context) error {
	t := time.NewTimer(or(c.Interval, defaultPollPeriod))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Connector) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// classify maps an error from portal, cas or netguard to a spec §6 status.
func classify(ctx context.Context, err error) Result {
	msg := sanitize(err)
	var netErr net.Error
	var ce *cas.Error
	var pe *portal.Error
	switch {
	case errors.Is(err, netguard.ErrBlocked):
		return Result{Status: InternalError, Message: msg}
	case ctx.Err() != nil, errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return Result{Status: TimedOut, Message: msg}
	case errors.As(err, &ce):
		return Result{Status: Status(ce.Kind), Message: msg}
	case errors.As(err, &pe):
		switch pe.Kind {
		case portal.KindChanged:
			return Result{Status: PortalChanged, Message: msg}
		case portal.KindRejected:
			return Result{Status: NeedsAttention, Message: msg}
		}
		return Result{Status: PortalUnavailable, Message: msg}
	}
	return Result{Status: InternalError, Message: msg}
}

// sanitize drops request URLs (which carry sessionId/ticket) from err text.
func sanitize(err error) string {
	msg := err.Error()
	var ue *url.Error
	for errors.As(err, &ue) {
		msg = strings.ReplaceAll(msg, ue.Error(), ue.Op+": "+ue.Err.Error())
		err = ue.Err
	}
	return msg
}

func or(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}
