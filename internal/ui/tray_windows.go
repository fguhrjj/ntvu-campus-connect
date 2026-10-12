//go:build windows

package ui

import (
	"context"
	"image"
	"image/color"
	"math"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"

	"campusconnect/internal/app"
	"campusconnect/internal/portal"
)

const appTitle = "校园网自动连接"

// Run shows the tray icon and runs runner until ctx is cancelled or the
// user picks 退出, and returns once runner.Run has returned. It must be
// called from the main goroutine, which walk pins as its UI thread. Run
// sets runner.OnState and opens the settings window when nothing usable is
// saved yet.
func Run(ctx context.Context, runner *app.Runner, store Store, loggerPath string) error {
	a, err := walk.InitApp()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cfg, cfgErr := store.Load()
	t, err := newTray(ctx, runner, store, loggerPath, cfgErr == nil && cfg.Autostart)
	if err != nil {
		return err
	}
	t.quit = cancel
	runner.OnState = func(st app.State) { a.Synchronize(func() { t.show(st) }) }

	done := make(chan error, 1)
	go func() {
		done <- runner.Run(ctx)
		cancel()
	}()
	go func() {
		<-ctx.Done()
		// walk panics disposing a form after Exit ends its layout
		// goroutines, so the settings window closes first.
		a.Synchronize(func() {
			if t.form != nil {
				t.form.dlg.Cancel()
			}
			a.Exit(0)
		})
	}()
	if cfgErr != nil || !cfg.Configured() {
		a.Synchronize(t.openSettings)
	}
	a.Run()

	t.dispose()
	cancel()
	return <-done
}

type tray struct {
	ctx     context.Context
	runner  *app.Runner
	store   Store
	logPath string
	quit    func()

	ni        *walk.NotifyIcon
	icons     [Red + 1]*walk.Icon // indexed by Color
	status    *walk.Action
	autostart *walk.Action
	presenter Presenter

	settingsOpen bool
	form         *settingsForm // the open settings window, nil when closed
}

func newTray(ctx context.Context, runner *app.Runner, store Store, logPath string, autostart bool) (*tray, error) {
	ni, err := walk.NewNotifyIcon()
	if err != nil {
		return nil, err
	}
	t := &tray{ctx: ctx, runner: runner, store: store, logPath: logPath, ni: ni}
	if err := t.build(autostart); err != nil {
		t.dispose()
		return nil, err
	}
	return t, nil
}

func (t *tray) build(autostart bool) error {
	dpi := t.ni.DPI()
	for c := range t.icons {
		icon, err := walk.NewIconFromImageForDPI(statusImage(Color(c), iconSize(dpi)), dpi)
		if err != nil {
			return err
		}
		t.icons[c] = icon
	}

	m := menu{list: t.ni.ContextMenu().Actions()}
	t.status = m.action("", nil)
	m.separator()
	m.action("立即连接", func() { t.runner.Trigger(app.TriggerManual) })
	m.action("打开认证页面", func() { t.open("无法打开认证页面", portal.BaseURL+"/") })
	m.action("设置…", t.openSettings)
	t.autostart = m.action("开机自启", t.toggleAutostart)
	m.action("打开日志", func() { t.open("无法打开日志", t.logPath) })
	m.separator()
	m.action("退出", func() { t.quit() })
	if m.err != nil {
		return m.err
	}
	for _, err := range []error{
		t.status.SetEnabled(false),
		t.autostart.SetCheckable(true),
		t.autostart.SetChecked(autostart),
	} {
		if err != nil {
			return err
		}
	}

	view, _ := t.presenter.Present(app.State{})
	if err := t.apply(view); err != nil {
		return err
	}
	return t.ni.SetVisible(true)
}

// show reflects st in the icon, tooltip and status item, and pops the
// balloon the presenter asks for.
func (t *tray) show(st app.State) {
	view, notice := t.presenter.Present(st)
	t.report(t.apply(view))
	if notice == nil {
		return
	}
	balloon := t.ni.ShowInfo
	switch notice.Level {
	case LevelWarning:
		balloon = t.ni.ShowWarning
	case LevelError:
		balloon = t.ni.ShowError
	}
	t.report(balloon(notice.Title, notice.Text))
}

func (t *tray) apply(v View) error {
	if err := t.ni.SetIcon(t.icons[v.Color]); err != nil {
		return err
	}
	if err := t.ni.SetToolTip(v.Tooltip); err != nil {
		return err
	}
	return t.status.SetText(v.Menu)
}

// toggleAutostart saves the check the menu item has just toggled to; the
// store applies it to the Run key. An open settings window follows, so
// saving it later does not undo the toggle.
func (t *tray) toggleAutostart() {
	want := t.autostart.Checked()
	cfg, err := t.store.Load()
	if err == nil {
		cfg.Autostart = want
		err = t.store.Save(cfg, "")
	}
	if err != nil {
		t.report(t.autostart.SetChecked(!want))
		t.fail("无法更改开机自启", err)
		return
	}
	if t.form != nil {
		t.form.autostart.SetChecked(want)
	}
}

// open hands target to its default handler (browser, text editor).
func (t *tray) open(what, target string) {
	if err := shellOpen(target); err != nil {
		t.fail(what, err)
	}
}

func shellOpen(target string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

func (t *tray) fail(what string, err error) {
	walk.MsgBox(nil, appTitle, what+"：\n"+err.Error(), walk.MsgBoxIconError|walk.MsgBoxOK|walk.MsgBoxSetForeground)
}

// report logs UI failures that leave the program working.
func (t *tray) report(err error) {
	if err != nil && t.runner.Logf != nil {
		t.runner.Logf("ui: %v", err)
	}
}

func (t *tray) dispose() {
	t.report(t.ni.Dispose())
	for _, icon := range t.icons {
		if icon != nil {
			icon.Dispose()
		}
	}
}

// menu appends actions, keeping the first error.
type menu struct {
	list *walk.ActionList
	err  error
}

func (m *menu) action(text string, fn func()) *walk.Action {
	a := walk.NewAction()
	if fn != nil {
		a.Triggered().Attach(fn)
	}
	if m.err == nil {
		m.err = a.SetText(text)
	}
	if m.err == nil {
		m.err = m.list.Add(a)
	}
	return a
}

func (m *menu) separator() {
	if m.err == nil {
		m.err = m.list.Add(walk.NewSeparatorAction())
	}
}

func iconSize(dpi int) int {
	if n := int(win.GetSystemMetricsForDpi(win.SM_CXSMICON, uint32(dpi))); n > 0 {
		return n
	}
	return walk.IntFrom96DPI(16, dpi)
}

var palette = [...]color.NRGBA{
	Gray:   {0x8a, 0x8f, 0x98, 0xff},
	Green:  {0x1e, 0xa4, 0x4b, 0xff},
	Yellow: {0xf2, 0xb3, 0x00, 0xff},
	Red:    {0xdc, 0x35, 0x2b, 0xff},
}

// statusImage draws an antialiased disc of colour c with a darker rim, so
// it stays visible on both light and dark taskbars.
func statusImage(c Color, size int) image.Image {
	fill := palette[c]
	rim := color.NRGBA{uint8(int(fill.R) * 3 / 5), uint8(int(fill.G) * 3 / 5), uint8(int(fill.B) * 3 / 5), 0xff}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	center := float64(size) / 2
	radius := center - 0.5
	width := math.Max(1, float64(size)/16)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := math.Hypot(float64(x)+0.5-center, float64(y)+0.5-center)
			cover := clamp01(radius - d + 0.5)
			if cover == 0 {
				continue
			}
			px := mix(fill, rim, clamp01(d-(radius-width)+0.5))
			px.A = uint8(cover*0xff + 0.5)
			img.SetNRGBA(x, y, px)
		}
	}
	return img
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	lerp := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return color.NRGBA{lerp(a.R, b.R), lerp(a.G, b.G), lerp(a.B, b.B), 0xff}
}

func clamp01(v float64) float64 {
	return math.Min(1, math.Max(0, v))
}
