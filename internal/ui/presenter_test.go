package ui

import (
	"strings"
	"testing"
	"time"

	"campusconnect/internal/app"
	"campusconnect/internal/config"
	"campusconnect/internal/flow"
	"campusconnect/internal/portal"
)

func TestPresenterMapsEveryStatusToTrayState(t *testing.T) {
	want := map[flow.Status]Color{
		flow.Connected:                    Green,
		flow.AlreadyOnline:                Green,
		flow.OnlineButInternetUnavailable: Yellow,
		flow.NotTargetNetwork:             Gray,
		flow.NetworkNotReady:              Yellow,
		flow.PortalUnavailable:            Yellow,
		flow.TimedOut:                     Yellow,
		flow.ConfigurationRequired:        Red,
		flow.AuthenticationRejected:       Red,
		flow.AccountLocked:                Red,
		flow.AccountRisk:                  Red,
		flow.CaptchaRequired:              Red,
		flow.ServiceNotFound:              Red,
		flow.NeedsAttention:               Red,
		flow.PortalChanged:                Red,
		flow.InternalError:                Red,
		"":                                Gray, // nothing checked yet
	}
	checked := time.Date(2026, 10, 10, 14, 3, 0, 0, time.Local)
	for status, color := range want {
		var p Presenter
		view, _ := p.Present(app.State{Status: status, CheckedAt: checked})
		if view.Color != color {
			t.Errorf("%q: color %v, want %v", status, view.Color, color)
		}
		if view.Label == "" || !strings.Contains(view.Menu, view.Label) || !strings.Contains(view.Tooltip, view.Label) {
			t.Errorf("%q: view %+v must show its label in menu and tooltip", status, view)
		}
		if status != "" && !strings.Contains(view.Tooltip, "14:03") {
			t.Errorf("%q: tooltip %q lacks last check time", status, view.Tooltip)
		}
		if len([]rune(view.Tooltip)) > 127 {
			t.Errorf("%q: tooltip longer than the shell limit: %q", status, view.Tooltip)
		}
	}
}

func TestPresenterShowsPauseInMenuAndNotice(t *testing.T) {
	var p Presenter
	view, n := p.Present(app.State{Status: flow.AuthenticationRejected, Paused: flow.AuthenticationRejected, Trigger: app.TriggerTimer, CheckedAt: time.Now()})
	if !strings.Contains(view.Menu, "已暂停自动登录") || n == nil || !strings.Contains(n.Text, "已暂停自动登录") {
		t.Fatalf("pause not shown: view %+v notice %+v", view, n)
	}
	if n.Level != LevelError {
		t.Fatalf("level %v, want error", n.Level)
	}
}

func TestPresenterSuppressesDuplicateProblemNotification(t *testing.T) {
	var p Presenter
	steps := []struct {
		status  flow.Status
		trigger app.Trigger
		notify  bool
	}{
		{flow.AlreadyOnline, app.TriggerStartup, false}, // nothing happened
		{flow.PortalUnavailable, app.TriggerTimer, true},
		{flow.TimedOut, app.TriggerTimer, false}, // same kind: temporarily unavailable
		{flow.PortalUnavailable, app.TriggerTimer, false},
		{flow.TimedOut, app.TriggerManual, true}, // the user asked: always answer
		{flow.Connected, app.TriggerTimer, true},
		{flow.AlreadyOnline, app.TriggerTimer, false},
		{flow.PortalUnavailable, app.TriggerTimer, true}, // state changed in between
		{flow.AuthenticationRejected, app.TriggerTimer, true},
		{flow.AccountLocked, app.TriggerTimer, false}, // same spec row
		{flow.AlreadyOnline, app.TriggerTimer, true},  // recovered
		{flow.Connected, app.TriggerTimer, true},      // a fresh login is always news
	}
	for i, s := range steps {
		_, n := p.Present(app.State{Status: s.status, Trigger: s.trigger, CheckedAt: time.Now()})
		if (n != nil) != s.notify {
			t.Fatalf("step %d %s/%s: notice %+v, want notify=%v", i, s.status, s.trigger, n, s.notify)
		}
	}
}

func TestPresenterStaysQuietWhenStartingOffCampus(t *testing.T) {
	var p Presenter
	if _, n := p.Present(app.State{Status: flow.NotTargetNetwork, Trigger: app.TriggerStartup, CheckedAt: time.Now()}); n != nil {
		t.Fatalf("startup off campus notified: %+v", n)
	}
	if _, n := p.Present(app.State{Status: flow.AlreadyOnline, Trigger: app.TriggerWLAN, CheckedAt: time.Now()}); n == nil {
		t.Fatal("joining the campus network must notify")
	}
}

func TestApplySettingsKeepsOrRequiresPassword(t *testing.T) {
	services := []portal.Service{{Label: "中国电信", Value: "CTCC"}, {Label: "中国联通", Value: "CUCC"}}
	saved := config.Default()
	saved.Username, saved.PasswordProtected = "old", "cipher"

	got, field, err := applySettings(saved, settingsInput{Username: " 20240000001 ", Service: 1, Autostart: true}, services)
	if err != nil {
		t.Fatalf("blank password with a saved one: %v (field %v)", err, field)
	}
	if got.Username != "20240000001" || got.ServiceValue != "CUCC" || got.ServiceLabel != "中国联通" || !got.Autostart || got.PasswordProtected != "cipher" {
		t.Fatalf("applied %+v", got)
	}

	fresh := config.Default()
	for _, c := range []struct {
		in    settingsInput
		field formField
	}{
		{settingsInput{Username: "  ", Password: "p", Service: 0}, fieldUsername},
		{settingsInput{Username: "u", Service: 0}, fieldPassword},
		{settingsInput{Username: "u", Password: "p", Service: -1}, fieldService},
		{settingsInput{Username: "u", Password: "p", Service: 2}, fieldService},
	} {
		if _, field, err := applySettings(fresh, c.in, services); err == nil || field != c.field {
			t.Errorf("%+v: field %v err %v, want field %v", c.in, field, err, c.field)
		}
	}
}

func TestPickServicePrefersCurrentThenUnicom(t *testing.T) {
	list := []portal.Service{{Label: "中国电信", Value: "CTCC"}, {Label: "中国联通", Value: "CUCC"}, {Label: "中国移动", Value: "CMCC"}}
	cases := []struct {
		list    []portal.Service
		current string
		want    int
	}{
		{list, "CMCC", 2},
		{list, "GONE", 1},
		{list, "", 1},
		{list[:1], "GONE", -1},
		{nil, "CUCC", -1},
	}
	for _, c := range cases {
		if got := pickService(c.list, c.current); got != c.want {
			t.Errorf("pickService(%d items, %q) = %d, want %d", len(c.list), c.current, got, c.want)
		}
	}
}
