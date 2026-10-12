//go:build windows

package ui

import (
	"context"
	"time"
	"unsafe"

	"github.com/tailscale/walk"
	d "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"campusconnect/internal/config"
	"campusconnect/internal/portal"
)

// servicesTimeout bounds the operator list fetch; the window stays usable
// with the saved operator meanwhile.
const servicesTimeout = 15 * time.Second

var (
	noteColor  = walk.RGB(0x5f, 0x63, 0x68)
	errorColor = walk.RGB(0xc4, 0x2b, 0x1c)
)

// settingsForm is the open settings window. All methods run on the UI
// thread.
type settingsForm struct {
	t         *tray
	dlg       *walk.Dialog
	username  *walk.LineEdit
	password  *walk.LineEdit
	service   *walk.ComboBox
	autostart *walk.CheckBox
	note      *walk.Label

	saved    config.Config
	services []portal.Service // what the combo box offers, same order

	// submitting is set while one Enter/保存 is handled. walk raises the
	// default button twice for a single IDOK (Dialog.WndProc, then
	// ContainerBase.WndProc); a second save in the same message could close
	// the window while a layout is pending, which panics inside walk.
	submitting bool
}

// openSettings shows the settings window and returns when it closes. A
// second request while it is open brings the open one to the front.
func (t *tray) openSettings() {
	if t.settingsOpen {
		if t.form != nil {
			win.SetForegroundWindow(t.form.dlg.Handle())
		}
		return
	}
	t.settingsOpen = true
	defer func() { t.settingsOpen = false }()

	cfg, err := t.store.Load()
	if err != nil {
		walk.MsgBox(nil, appTitle, "设置文件无法读取，保存后将被覆盖：\n"+err.Error(),
			walk.MsgBoxIconWarning|walk.MsgBoxOK|walk.MsgBoxSetForeground)
		cfg = config.Default()
	}
	if t.ctx.Err() != nil {
		return // exiting; a new form would outlive walk's layout goroutines
	}
	f := &settingsForm{t: t, saved: cfg}
	if err := f.create(); err != nil {
		t.fail("无法打开设置窗口", err)
		return
	}
	t.form = f
	defer func() { t.form = nil }()
	go f.fetchServices()
	f.dlg.Run()
}

func (f *settingsForm) create() error {
	var save, cancel *walk.PushButton
	passwordName, passwordCue := "密码", ""
	if f.saved.PasswordProtected != "" {
		passwordName, passwordCue = "密码（已保存，留空表示不修改）", "已保存，留空表示不修改"
	}
	err := d.Dialog{
		AssignTo: &f.dlg,
		Title:    "设置 - " + appTitle,
		Font:     d.Font{Family: "Microsoft YaHei UI", PointSize: 9},
		// walk leaves the dialog client area without a brush; WM_CTLCOLORSTATIC
		// then falls through to DefWindowProc, which drops the note's TextColor.
		Background:    d.SystemColorBrush{Color: walk.SysColorBtnFace},
		MinSize:       d.Size{Width: 380},
		FixedSize:     true,
		DefaultButton: &save,
		CancelButton:  &cancel,
		Layout:        d.VBox{MarginsZero: true},
		Children: []d.Widget{
			// One top-level child: walk reparents each top-level child into the
			// client area at the top of the z-order, which reverses Tab order.
			d.Composite{
				Layout: d.VBox{Margins: d.Margins{Left: 14, Top: 14, Right: 14, Bottom: 12}, Spacing: 10},
				Children: []d.Widget{
					d.Composite{
						Layout: d.Grid{Columns: 2, MarginsZero: true, Spacing: 8},
						Children: []d.Widget{
							d.Label{Text: "学号(&U)："},
							d.LineEdit{AssignTo: &f.username, Text: f.saved.Username, MaxLength: 64,
								Accessibility: d.Accessibility{Name: "学号"}},
							d.Label{Text: "密码(&P)："},
							d.LineEdit{AssignTo: &f.password, PasswordMode: true, CueBanner: passwordCue, MaxLength: 128,
								Accessibility: d.Accessibility{Name: passwordName}},
							d.Label{Text: "运营商(&O)："},
							d.ComboBox{AssignTo: &f.service, Model: []string{},
								Accessibility: d.Accessibility{Name: "运营商"}},
							d.CheckBox{AssignTo: &f.autostart, Text: "开机自启(&A)", Checked: f.saved.Autostart, Row: 3, Column: 1,
								Accessibility: d.Accessibility{Name: "开机自启"}},
						},
					},
					d.Label{AssignTo: &f.note, Text: "正在从认证页面获取运营商列表…", NoPrefix: true, TextColor: noteColor},
					d.Composite{
						Layout: d.HBox{MarginsZero: true},
						Children: []d.Widget{
							d.HSpacer{},
							d.PushButton{AssignTo: &save, Text: "保存", OnClicked: f.save},
							d.PushButton{AssignTo: &cancel, Text: "取消", OnClicked: func() { f.dlg.Cancel() }},
						},
					},
				},
			},
		},
	}.Create(nil)
	if err != nil {
		return err
	}
	f.setServices(savedServices(f.saved))
	f.dlg.Starting().Attach(func() {
		f.t.report(centerOnMonitor(f.dlg))
		win.SetForegroundWindow(f.dlg.Handle())
	})
	return nil
}

// fetchServices replaces the saved operator with the portal's live list
// when the portal answers (on campus), and says so when it does not.
func (f *settingsForm) fetchServices() {
	ctx, cancel := context.WithTimeout(f.t.ctx, servicesTimeout)
	list, err := f.t.store.Services(ctx)
	cancel()
	walk.App().Synchronize(func() {
		if f.t.form != f {
			return
		}
		if err != nil || len(list) == 0 {
			if err != nil {
				f.t.report(err)
			}
			f.setNote("未能获取运营商列表，仅显示已保存的运营商", false)
			return
		}
		f.setServices(list)
		f.setNote("运营商列表已从认证页面获取", false)
	})
}

// setServices offers list, keeping the current choice when it is still
// offered (pickService).
func (f *settingsForm) setServices(list []portal.Service) {
	current := f.saved.ServiceValue
	if i := f.service.CurrentIndex(); i >= 0 && i < len(f.services) {
		current = f.services[i].Value
	}
	labels := make([]string, len(list))
	for i, s := range list {
		labels[i] = s.Label
		if labels[i] == "" {
			labels[i] = s.Value
		}
	}
	f.services = list
	f.t.report(f.service.SetModel(labels))
	f.t.report(f.service.SetCurrentIndex(pickService(list, current)))
}

func (f *settingsForm) save() {
	if f.submitting {
		return
	}
	f.submitting = true
	// Runs after the current message, so it clears only once both of
	// walk's IDOK handlers have returned.
	walk.App().Synchronize(func() { f.submitting = false })
	in := settingsInput{
		Username:  f.username.Text(),
		Password:  f.password.Text(),
		Service:   f.service.CurrentIndex(),
		Autostart: f.autostart.Checked(),
	}
	cfg, field, err := applySettings(f.saved, in, f.services)
	if err != nil {
		f.setNote(err.Error(), true)
		f.focus(field)
		return
	}
	if err := f.t.store.Save(cfg, in.Password); err != nil {
		f.setNote("保存失败："+err.Error(), true)
		return
	}
	f.t.report(f.t.autostart.SetChecked(cfg.Autostart))
	f.dlg.Accept()
	f.t.runner.SettingsSaved()
}

func (f *settingsForm) focus(field formField) {
	var w walk.Window
	switch field {
	case fieldUsername:
		w = f.username
	case fieldPassword:
		w = f.password
	case fieldService:
		w = f.service
	default:
		return
	}
	f.t.report(w.SetFocus())
}

func (f *settingsForm) setNote(text string, isError bool) {
	c := noteColor
	if isError {
		c = errorColor
	}
	f.note.SetTextColor(c)
	f.t.report(f.note.SetText(text))
}

// centerOnMonitor centres dlg in the work area of its monitor; walk leaves
// owner-less dialogs at the default window position.
func centerOnMonitor(dlg *walk.Dialog) error {
	mi := win.MONITORINFO{CbSize: uint32(unsafe.Sizeof(win.MONITORINFO{}))}
	if !win.GetMonitorInfo(win.MonitorFromWindow(dlg.Handle(), win.MONITOR_DEFAULTTONEAREST), &mi) {
		return nil
	}
	work := mi.RcWork
	b := dlg.BoundsPixels()
	b.X = int(work.Left) + (int(work.Right-work.Left)-b.Width)/2
	b.Y = int(work.Top) + (int(work.Bottom-work.Top)-b.Height)/2
	return dlg.SetBoundsPixels(b)
}
