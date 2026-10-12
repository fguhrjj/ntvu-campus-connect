// Command campusconnect keeps the campus WLAN signed in from the
// notification area (spec §3). This file wires the packages together without
// touching Windows APIs so the wiring is testable on any platform.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"campusconnect/internal/cas"
	"campusconnect/internal/config"
	"campusconnect/internal/flow"
	"campusconnect/internal/logx"
	"campusconnect/internal/netguard"
	"campusconnect/internal/portal"
)

// Bounds from spec §4.
const (
	requestTimeout = 7 * time.Second
	attemptTimeout = 60 * time.Second
)

var (
	portalBase  = mustParse(portal.BaseURL)
	casNapBase  = mustParse(cas.NapBaseURL)
	casAuthBase = mustParse(cas.AuthBaseURL)
)

func mustParse(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

// wiring holds the process-wide dependencies. It is the ui.Store and
// provides the runner's Connect; main fills in the Windows implementations.
type wiring struct {
	configPath   string
	protector    config.Protector
	log          *logx.Logger
	transport    http.RoundTripper
	currentSSID  func() (string, error)
	setAutostart func(enabled bool) error
}

// httpClient returns a fresh allowlisted client (empty cookie jar) that logs
// every request it sees.
func (w *wiring) httpClient() *netguard.Client {
	c := netguard.NewClient(netguard.ProductionPolicy(), w.transport, requestTimeout)
	c.Observe = w.observe
	return c
}

func (w *wiring) observe(e netguard.Event) {
	target := e.Host + e.Path
	switch {
	case e.Blocked:
		w.log.Eventf("已拦截请求：%s %s", e.Method, target)
	case e.Err != nil:
		// *url.Error repeats the full URL including its query; keep only the cause.
		err := e.Err
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		w.log.Eventf("请求失败：%s %s %dms：%v", e.Method, target, e.Elapsed.Milliseconds(), err)
	default:
		w.log.Request(e.Method, target, e.Status, e.Elapsed)
	}
}

// connect performs one attempt with settings read fresh from disk and a new
// HTTP client, so cookies never outlive the attempt.
func (w *wiring) connect(ctx context.Context, mode flow.Mode) flow.Result {
	cfg, err := config.Load(w.configPath, w.protector)
	if err != nil {
		w.log.Eventf("读取设置失败，按未配置处理：%v", err)
		cfg = config.Default()
	}
	w.log.SetUsername(cfg.Username)

	hc := w.httpClient()
	c := &flow.Connector{
		Portal:        portal.NewClient(hc, portalBase),
		Authenticator: cas.NewAuthenticator(hc, casNapBase, casAuthBase),
		ServiceValue:  cfg.ServiceValue,
		Probe:         portal.InternetProbe{HTTP: hc, URL: portal.ProbeURL},
		TargetSSID:    cfg.SSID,
		CurrentSSID:   w.loggedSSID,
		Timeout:       attemptTimeout,
		Logf:          w.log.Eventf,
	}
	if cfg.Configured() {
		// Decrypted only when the portal asks for sign-in.
		c.Credentials = func() (cas.Credentials, error) {
			password, err := cfg.Password(w.protector)
			return cas.Credentials{Username: cfg.Username, Password: password}, err
		}
	}
	return c.Connect(ctx, mode)
}

// loggedSSID reads the SSID and records it; the flow logs read failures.
func (w *wiring) loggedSSID() (string, error) {
	ssid, err := w.currentSSID()
	switch {
	case err != nil:
	case ssid == "":
		w.log.Event("当前 WLAN：未连接")
	default:
		w.log.Eventf("当前 WLAN：%q", ssid)
	}
	return ssid, err
}

// Load reads the saved settings (Default when none are saved).
func (w *wiring) Load() (config.Config, error) {
	return config.Load(w.configPath, w.protector)
}

// Save writes the settings, then makes the Run key match cfg.Autostart.
func (w *wiring) Save(cfg config.Config, newPassword string) error {
	if err := config.Save(w.configPath, cfg, newPassword, w.protector); err != nil {
		return err
	}
	if err := w.setAutostart(cfg.Autostart); err != nil {
		return fmt.Errorf("开机自启设置失败：%w", err)
	}
	return nil
}

// Services lists the operators offered by a freshly discovered portal session.
func (w *wiring) Services(ctx context.Context) ([]portal.Service, error) {
	p := portal.NewClient(w.httpClient(), portalBase)
	sess, err := p.Discover(ctx)
	if err != nil {
		return nil, err
	}
	return p.Services(ctx, sess.ID)
}
