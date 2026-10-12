//go:build windows

package main

import (
	"context"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"campusconnect/internal/app"
	"campusconnect/internal/autostart"
	"campusconnect/internal/config"
	"campusconnect/internal/logx"
	"campusconnect/internal/netguard"
	"campusconnect/internal/power"
	"campusconnect/internal/ui"
	"campusconnect/internal/wlan"
)

const (
	appTitle = "校园网自动连接"
	appDir   = "CampusConnect"
	runValue = "CampusConnect"
)

func main() { os.Exit(run()) }

// run returns the exit code; every failure is shown to the user because a
// GUI-subsystem process has no console.
func run() int {
	release, acquired, err := app.AcquireSingleInstance()
	if err != nil {
		showError("无法检查程序是否已在运行：" + err.Error())
		return 1
	}
	if !acquired {
		messageBox(appTitle+"已在运行", windows.MB_ICONINFORMATION)
		return 0
	}
	defer release()

	configDir, err := os.UserConfigDir() // %APPDATA%
	if err != nil {
		showError("无法定位设置目录：" + err.Error())
		return 1
	}
	logDir, err := os.UserCacheDir() // %LOCALAPPDATA%
	if err != nil {
		showError("无法定位日志目录：" + err.Error())
		return 1
	}
	log, err := logx.NewLogger(filepath.Join(logDir, appDir, "campus.log"))
	if err != nil {
		showError("无法创建日志文件：" + err.Error())
		return 1
	}
	defer log.Close()

	w := &wiring{
		configPath:   filepath.Join(configDir, appDir, "config.json"),
		protector:    config.NewProtector(),
		log:          log,
		transport:    netguard.DefaultTransport(),
		currentSSID:  wlan.CurrentSSID,
		setAutostart: setAutostart,
	}
	// A load error is logged by every attempt and opens settings in ui.Run.
	cfg, err := w.Load()
	if err != nil {
		cfg = config.Default()
	}
	runner := &app.Runner{
		Connect:         w.connect,
		Logf:            log.Eventf,
		SubscribeWLAN:   wlan.Subscribe,
		SubscribeResume: power.SubscribeResume,
		TargetSSID:      cfg.SSID,
	}
	if err := ui.Run(context.Background(), runner, w, log.Path()); err != nil {
		log.Eventf("界面运行失败：%v", err)
		showError("界面运行失败：" + err.Error())
		return 1
	}
	return 0
}

// setAutostart points the Run value at this executable, quoted for spaces.
func setAutostart(enabled bool) error {
	if !enabled {
		return autostart.Disable(runValue)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return autostart.Enable(runValue, `"`+exe+`"`)
}

func showError(text string) { messageBox(text, windows.MB_ICONERROR) }

func messageBox(text string, icon uint32) {
	t, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	c, _ := windows.UTF16PtrFromString(appTitle)
	windows.MessageBox(0, t, c, windows.MB_OK|icon|windows.MB_SETFOREGROUND)
}
