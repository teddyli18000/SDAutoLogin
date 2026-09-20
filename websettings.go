package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	settingsIdleTimeout = 15 * time.Minute
	logTailLines        = 200
)

// settingsServer serves the settings page on loopback only. Every request
// carries a token that is generated when the server starts, so another local
// process cannot read or change the configuration by accident.
type settingsServer struct {
	state  *appState
	appDir string
	onQuit func()

	mu      sync.Mutex
	srv     *http.Server
	url     string
	token   string
	lastHit atomic.Int64
}

func newSettingsServer(state *appState, appDir string, onQuit func()) *settingsServer {
	return &settingsServer{state: state, appDir: appDir, onQuit: onQuit}
}

// URL starts the server on first use and returns the tokenised address.
func (s *settingsServer) URL() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.url != "" {
		return s.url, nil
	}

	token, err := randomToken()
	if err != nil {
		return "", err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen on loopback: %w", err)
	}

	s.token = token
	s.url = fmt.Sprintf("http://127.0.0.1:%d/?token=%s", listener.Addr().(*net.TCPAddr).Port, token)
	s.srv = &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.lastHit.Store(time.Now().UnixNano())

	go func() {
		if err := s.srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			Log.Error("settings service stopped: %v", err)
		}
	}()
	go s.reapWhenIdle()

	Log.Info("settings page available at %s", s.url)
	return s.url, nil
}

// OpenURL starts the server if needed and hands the address to the browser.
func (s *settingsServer) OpenURL() (string, error) {
	url, err := s.URL()
	if err != nil {
		return "", err
	}
	if err := openExternal(url); err != nil {
		Log.Warn("opening the settings page: %v", err)
		return url, err
	}
	return url, nil
}

func (s *settingsServer) Stop() {
	s.mu.Lock()
	srv := s.srv
	s.srv, s.url, s.token = nil, "", ""
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
		Log.Debug("settings service stopped")
	}
}

// reapWhenIdle releases the port when nobody has looked at the page for a
// while, so the process does not keep a socket and a UI thread around forever.
func (s *settingsServer) reapWhenIdle() {
	for {
		time.Sleep(time.Minute)
		last := time.Unix(0, s.lastHit.Load())
		if time.Since(last) < settingsIdleTimeout {
			continue
		}
		s.mu.Lock()
		active := s.url != ""
		s.mu.Unlock()
		if active {
			Log.Info("settings page idle for %s, shutting the service down", settingsIdleTimeout)
			s.Stop()
		}
		return
	}
}

func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (s *settingsServer) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(webAssetsDir()))))
	mux.HandleFunc("/api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/action", s.handleAction)
	return mux
}

// authorised accepts the token from the query string, which the browser uses
// on first load, or from a header, which the page uses afterwards. Requiring a
// custom header for writes also makes cross site requests impossible.
func (s *settingsServer) authorised(r *http.Request) bool {
	s.lastHit.Store(time.Now().UnixNano())
	s.mu.Lock()
	want := s.token
	s.mu.Unlock()
	if want == "" {
		return false
	}
	if got := r.Header.Get("X-Auth-Token"); got == want {
		return true
	}
	return r.URL.Query().Get("token") == want
}

func (s *settingsServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !s.authorised(r) {
		http.Error(w, "invalid or missing token", http.StatusForbidden)
		return
	}
	serveAsset(w, r, "index.html", "text/html; charset=utf-8")
}

func (s *settingsServer) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "invalid or missing token", http.StatusForbidden)
		return
	}
	cfg := s.state.Config()
	lang := Lang(r.URL.Query().Get("lang"))
	if lang != LangZH && lang != LangEN {
		lang = CurrentLang()
	}
	writeJSON(w, map[string]any{
		"version":     appVersion,
		"repo":        appRepo,
		"platform":    platformName(),
		"lang":        string(lang),
		"langSetting": cfg.Language,
		"i18n":        ALL(lang),
		"config":      cfg,
		"operators":   Operators,
		"status":      statusPayload(s.state.Snapshot()),
		"autostart":   autostartEnabled(),
		"configured":  cfg.Configured(),
		"paths": map[string]string{
			"config": cfg.Path(),
			"log":    Log.Path(),
		},
	})
}

func (s *settingsServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "invalid or missing token", http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]any{
		"status":    statusPayload(s.state.Snapshot()),
		"autostart": autostartEnabled(),
	})
}

func (s *settingsServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorised(r) {
		http.Error(w, "invalid or missing token", http.StatusForbidden)
		return
	}

	next := *s.state.Config()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&next); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"ok":     false,
			"errors": map[string]string{"_": "err.badRequest"},
		})
		return
	}
	if errs := validateConfig(&next); len(errs) > 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "errors": errs})
		return
	}
	next.ProbeURLs = compactProbes(next.ProbeURLs)
	if err := s.state.Update(&next); err != nil {
		Log.Error("saving settings from the page: %v", err)
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{
			"ok":     false,
			"errors": map[string]string{"_": "err.saveFailed"},
		})
		return
	}
	Log.Info("settings page saved the config: %s", next.Summary())
	writeJSON(w, map[string]any{"ok": true, "config": next})
}

func (s *settingsServer) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorised(r) {
		http.Error(w, "invalid or missing token", http.StatusForbidden)
		return
	}

	var req struct {
		Action string `json:"action"`
		Value  bool   `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "err.badRequest"})
		return
	}

	switch req.Action {
	case "check":
		Log.Info("manual check requested from the settings page")
		s.state.Trigger()
		writeJSON(w, map[string]any{"ok": true})

	case "testLogin":
		Log.Info("login self test requested from the settings page")
		test := s.state.TestLogin()
		writeJSON(w, map[string]any{
			"ok":     true,
			"before": statusPayload(snapshotFromStatus(test.Before)),
			"result": test.Result,
			"after":  statusPayload(snapshotFromStatus(test.After)),
		})

	case "reload":
		next, err := s.state.Reload()
		if err != nil {
			Log.Error("reloading config from the page: %v", err)
			writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "err.loadFailed"})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "config": next})

	case "openConfig":
		openExternal(s.state.Config().Path())
		writeJSON(w, map[string]any{"ok": true})

	case "openLog":
		if p := Log.Path(); p != "" {
			openExternal(p)
		}
		writeJSON(w, map[string]any{"ok": true})

	case "logTail":
		writeJSON(w, map[string]any{"ok": true, "lines": tailLog(logTailLines)})

	case "autostart":
		Log.Info("autostart changed from the settings page: %v", req.Value)
		ok := setAutostart(req.Value)
		writeJSON(w, map[string]any{"ok": ok, "autostart": autostartEnabled()})

	case "quit":
		Log.Info("exit requested from the settings page")
		writeJSON(w, map[string]any{"ok": true})
		if s.onQuit != nil {
			go s.onQuit()
		}

	default:
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "err.unknownAction"})
	}
}

// tailLog returns the last n lines of the log file, oldest first.
func tailLog(n int) []string {
	path := Log.Path()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
