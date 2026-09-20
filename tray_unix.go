//go:build linux || darwin

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"fyne.io/systray"
)

// runUI provides the Linux / macOS tray through fyne.io/systray, since the
// standard library cannot create one. On Linux the library uses a pure Go
// D-Bus StatusNotifierItem backend, on macOS it needs cgo and therefore the
// Xcode command line tools at build time.
func runUI(svc *Service, cfg *Config, appDir string) {
	systray.Run(func() {
		trayReady(svc, cfg, appDir)
	}, func() {
		svc.Stop()
		Log.Info("tray stopped")
	})
}

type unixTray struct {
	mu      sync.Mutex
	cfg     *Config
	mStatus *systray.MenuItem
	mSince  *systray.MenuItem

	iconOnline  []byte
	iconOffline []byte
	iconBusy    []byte
	iconError   []byte
	lastState   State
}

func trayReady(svc *Service, cfg *Config, appDir string) {
	t := &unixTray{
		cfg:         cfg,
		lastState:   StateUnknown,
		iconOnline:  statusIcon(color.RGBA{R: 0x2E, G: 0xA0, B: 0x43, A: 0xFF}),
		iconOffline: statusIcon(color.RGBA{R: 0x88, G: 0x88, B: 0x88, A: 0xFF}),
		iconBusy:    statusIcon(color.RGBA{R: 0xE8, G: 0x9B, B: 0x1E, A: 0xFF}),
		iconError:   statusIcon(color.RGBA{R: 0xD1, G: 0x3A, B: 0x2E, A: 0xFF}),
	}

	systray.SetIcon(t.iconOffline)
	systray.SetTooltip(appTitle + " - checking")

	t.mStatus = systray.AddMenuItem("Status: checking", "")
	t.mStatus.Disable()
	t.mSince = systray.AddMenuItem("Last login: -", "")
	t.mSince.Disable()

	systray.AddSeparator()
	mNow := systray.AddMenuItem("Check and log in now", "Run one check immediately")
	systray.AddSeparator()
	mReload := systray.AddMenuItem("Reload config", "Re-read account settings from config.json")
	mOpenCfg := systray.AddMenuItem("Open config file", cfg.Path())
	mAutostart := systray.AddMenuItemCheckbox("Run at startup", "Start automatically after login", autostartEnabled())
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Exit", "Quit the program, does not log the session out")

	go func() {
		for range mNow.ClickedCh {
			Log.Info("manual check requested")
			svc.Trigger()
		}
	}()
	go func() {
		for range mReload.ClickedCh {
			next, err := LoadConfig(cfg.Path())
			if err != nil {
				Log.Error("reloading config: %v", err)
				continue
			}
			t.cfg = next
			svc.Restart(next)
			svc.Trigger()
			Log.Info("config reloaded")
		}
	}()
	go func() {
		for range mOpenCfg.ClickedCh {
			openInEditor(t.cfg.Path())
		}
	}()
	go func() {
		for range mAutostart.ClickedCh {
			on := !mAutostart.Checked()
			if !setAutostart(on) {
				Log.Warn("updating autostart entry failed")
				continue
			}
			if on {
				mAutostart.Check()
			} else {
				mAutostart.Uncheck()
			}
			Log.Info("autostart set to %v", on)
		}
	}()
	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()

	svc.SetListener(t.onStatus)
	svc.Start()

	if !cfg.Configured() {
		Log.Warn("no account configured yet, opening %s", cfg.Path())
		openInEditor(cfg.Path())
	}
}

func (t *unixTray) onStatus(s Snapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if s.State != t.lastState {
		t.lastState = s.State
		switch s.State {
		case StateOnline:
			systray.SetIcon(t.iconOnline)
		case StateLoggingIn, StateOffline:
			systray.SetIcon(t.iconBusy)
		case StateError:
			systray.SetIcon(t.iconError)
		default:
			systray.SetIcon(t.iconOffline)
		}
	}
	systray.SetTooltip(t.tooltip(s))
	t.mStatus.SetTitle("Status: " + describeSnapshot(s))
	if s.LastLoginAt != "" {
		t.mSince.SetTitle("Last login: " + s.LastLoginAt)
	}
}

func (t *unixTray) tooltip(s Snapshot) string {
	out := appTitle + "\nStatus: " + describeSnapshot(s)
	if s.InternetChecked {
		if s.InternetOK {
			out += "\nInternet: reachable"
		} else {
			out += "\nInternet: unreachable"
		}
	}
	return out
}

// openInEditor opens the config file with the platform default handler.
func openInEditor(path string) {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", "-t", path)
	case os.Getenv("EDITOR") != "":
		cmd = exec.Command(os.Getenv("EDITOR"), path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		Log.Warn("opening config file: %v", err)
	}
}

// statusIcon renders a 16x16 filled circle as PNG, avoiding an icon asset.
func statusIcon(c color.RGBA) []byte {
	const size = 16
	const cx, cy, r = 7.5, 7.5, 6.5
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}
