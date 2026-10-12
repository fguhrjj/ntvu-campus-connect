// Package ui is the tray icon and settings window (spec §7). Presenter and
// the settings form logic are portable and unit-tested; the walk code lives
// in the *_windows.go files.
package ui

import (
	"strings"

	"campusconnect/internal/app"
	"campusconnect/internal/flow"
)

// Color is the tray icon colour (spec §6).
type Color int

const (
	Gray Color = iota
	Green
	Yellow
	Red
)

// Level selects the balloon icon.
type Level int

const (
	LevelInfo Level = iota
	LevelWarning
	LevelError
)

// View is what the tray shows for one state.
type View struct {
	Color   Color
	Label   string // short status, e.g. "已在线"
	Menu    string // text of the disabled status menu item
	Tooltip string // hover text: status and last check time
}

// Notice is a balloon to show.
type Notice struct {
	Level Level
	Title string
	Text  string
}

// kind groups statuses that count as "the same kind of problem" (spec §7):
// one row of the spec §6 table.
type kind int

const (
	kindNone kind = iota
	kindOnline
	kindNoInternet
	kindOffCampus
	kindUnavailable
	kindConfig
	kindAuth
	kindService
	kindAttention
	kindBroken
)

type statusInfo struct {
	color Color
	kind  kind
	label string
	hint  string // what the user can do; "" when nothing is needed
}

var statuses = map[flow.Status]statusInfo{
	flow.Connected:                    {Green, kindOnline, "已连接", ""},
	flow.AlreadyOnline:                {Green, kindOnline, "已在线", ""},
	flow.OnlineButInternetUnavailable: {Yellow, kindNoInternet, "已在线，但外网不通", ""},
	flow.NotTargetNetwork:             {Gray, kindOffCampus, "未连接校园网", ""},
	flow.NetworkNotReady:              {Yellow, kindUnavailable, "网络未就绪", "稍后自动重试"},
	flow.PortalUnavailable:            {Yellow, kindUnavailable, "认证门户暂时不可用", "稍后自动重试"},
	flow.TimedOut:                     {Yellow, kindUnavailable, "连接超时", "稍后自动重试"},
	flow.ConfigurationRequired:        {Red, kindConfig, "需要设置学号和密码", "请在“设置…”中填写"},
	flow.AuthenticationRejected:       {Red, kindAuth, "学号或密码被拒绝", "请在“设置…”中检查学号和密码"},
	flow.AccountLocked:                {Red, kindAuth, "账号已被锁定", "请稍后通过“打开认证页面”确认账号状态"},
	flow.AccountRisk:                  {Red, kindAuth, "账号需要安全验证", "请通过“打开认证页面”手动登录一次"},
	flow.CaptchaRequired:              {Red, kindAuth, "登录需要验证码", "请通过“打开认证页面”手动登录一次"},
	flow.ServiceNotFound:              {Red, kindService, "运营商不在列表中", "请在“设置…”中重新选择运营商"},
	flow.NeedsAttention:               {Red, kindAttention, "需要手动处理", "请通过“打开认证页面”手动登录，并查看日志"},
	flow.PortalChanged:                {Red, kindBroken, "认证门户结构已变化", "请查看日志"},
	flow.InternalError:                {Red, kindBroken, "程序错误", "请查看日志"},
}

var checking = statusInfo{Gray, kindNone, "正在检查…", ""}

const pausedNote = "已暂停自动登录"

// Presenter turns runner states into tray views and decides which states
// deserve a balloon. Not safe for concurrent use; the UI thread owns it.
type Presenter struct {
	last kind
}

// Present returns the view for st and the balloon to show, if any.
//
// A balloon appears when the kind of state changes, never twice in a row
// for the same kind (spec §7), with two exceptions: a fresh login is always
// reported, and so is every answer to the user's own request (立即连接 or
// saving settings). The first state after startup stays quiet when it is
// unremarkable (already online or off campus).
func (p *Presenter) Present(st app.State) (View, *Notice) {
	info, ok := statuses[st.Status]
	if !ok {
		info = checking
	}
	label := info.label
	if st.Paused != "" {
		label += " · " + pausedNote
	}
	view := View{Color: info.color, Label: info.label, Menu: "状态：" + label, Tooltip: "校园网：" + label}
	if !st.CheckedAt.IsZero() {
		view.Tooltip += "\n最后检查 " + st.CheckedAt.Format("15:04")
	}
	if !ok {
		return view, nil
	}

	asked := st.Trigger == app.TriggerManual || st.Trigger == app.TriggerSettings
	changed := info.kind != p.last
	first := p.last == kindNone
	p.last = info.kind
	switch {
	case asked, st.Status == flow.Connected:
	case first && (info.kind == kindOnline || info.kind == kindOffCampus):
		return view, nil
	case !changed:
		return view, nil
	}
	return view, &Notice{Level: level(info.color), Title: "校园网：" + info.label, Text: noticeText(st, info)}
}

func level(c Color) Level {
	switch c {
	case Red:
		return LevelError
	case Yellow:
		return LevelWarning
	}
	return LevelInfo
}

func noticeText(st app.State, info statusInfo) string {
	var lines []string
	if st.Message != "" {
		lines = append(lines, st.Message)
	}
	if info.hint != "" {
		lines = append(lines, info.hint)
	}
	if st.Paused != "" {
		lines = append(lines, pausedNote+"，修改并保存设置后恢复")
	}
	if len(lines) == 0 {
		return info.label
	}
	return strings.Join(lines, "\n")
}
