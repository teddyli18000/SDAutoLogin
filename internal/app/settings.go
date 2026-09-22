package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sdautologin/internal/config"
	"sdautologin/internal/i18n"
	"sdautologin/internal/logging"
	"sdautologin/internal/platform"
	"sdautologin/internal/webui"
)

const (
	settingsIdleTimeout = 15 * time.Minute
	logTailLines        = 200
)

// Server serves the settings page. Every request carries a token that is
// generated when the server starts, so a program that has not been handed the
// address cannot read or change the configuration by accident.
type Server struct {
	state  *AppState
	onQuit func()
	// addr is where to listen, empty for loopback on a port the system picks.
	addr string

	mu      sync.Mutex
	srv     *http.Server
	url     string
	token   string
	lastHit atomic.Int64
}

// NewServer returns a settings server for state. addr is the address to listen
// on: an empty one means loopback on a port the system picks, started on first
// use and shut down again once it has been idle. An address the caller names is
// a management endpoint that was asked for, so it starts at once and stays up.
// onQuit, when set, is called after the page asks the program to exit.
func NewServer(state *AppState, addr string, onQuit func()) *Server {
	return &Server{state: state, addr: addr, onQuit: onQuit}
}

// URL starts the server on first use and returns the tokenised address.
func (s *Server) URL() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.url != "" {
		return s.url, nil
	}

	addr := s.addr
	onDemand := addr == ""
	if onDemand {
		addr = "127.0.0.1:0"
	}

	token, err := randomToken()
	if err != nil {
		return "", err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("listen on %s: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.token = token
	s.url = urlFor(listener.Addr(), token)
	s.srv = srv
	s.lastHit.Store(time.Now().UnixNano())

	if onDemand {
		// The tray opens the page when it is wanted, so the port is released
		// again once it has gone unused for a while.
		go s.reapWhenIdle()
	} else {
		if host, _, err := net.SplitHostPort(listener.Addr().String()); err == nil && !isLoopback(host) {
			logging.Log.Warn("the settings page is reachable from the network on %s; the token in its address is the only protection, so keep it on a trusted network or behind a tunnel", listener.Addr())
		}
		logging.Log.Info("settings page for remote management at %s, staying up until the program stops", s.url)
	}

	// The local srv is what this goroutine serves on. Stop clears the field, so
	// reading it from here would race with a shutdown that comes right after the
	// start.
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			logging.Log.Error("settings service stopped: %v", err)
		}
	}()

	if onDemand {
		logging.Log.Info("settings page available at %s", s.url)
	}
	return s.url, nil
}

// urlFor builds the address to hand to a browser out of the address the server
// actually bound.
func urlFor(addr net.Addr, token string) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		host, port = addr.String(), ""
	}
	host = hostForURL(host)
	if strings.Contains(host, ":") {
		// An IPv6 literal needs brackets in a URL.
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%s/?token=%s", host, port, token)
}

// hostForURL picks the host to put in that address. A listener on every
// interface has no useful host of its own, so the loopback address is used; an
// operator reaching the page from elsewhere replaces it with the address of this
// machine, and the log line around the page says so.
func hostForURL(host string) string {
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	}
	return host
}

// isLoopback reports whether host names this machine only.
func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

// OpenURL starts the server if needed and hands the address to the browser.
func (s *Server) OpenURL() (string, error) {
	return s.open(platform.OpenExternal)
}

// OpenAppWindow starts the server if needed and hands the address to a browser
// window that has none of the browser chrome, so the settings page looks like an
// application of its own. See platform.OpenAppWindow for what it falls back to.
func (s *Server) OpenAppWindow() (string, error) {
	return s.open(platform.OpenAppWindow)
}

// open serves the page and hands the address to opener. The address comes back
// even when opening it failed, so the caller can show it to the user.
func (s *Server) open(opener func(string) error) (string, error) {
	url, err := s.URL()
	if err != nil {
		return "", err
	}
	if err := opener(url); err != nil {
		logging.Log.Warn("opening the settings page: %v", err)
		return url, err
	}
	return url, nil
}

func (s *Server) Stop() {
	s.mu.Lock()
	srv := s.srv
	s.srv, s.url, s.token = nil, "", ""
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
		logging.Log.Debug("settings service stopped")
	}
}

// reapWhenIdle releases the port once the page has gone unused for a while, so
// the process does not keep a socket and a UI thread around forever.
func (s *Server) reapWhenIdle() {
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
			logging.Log.Info("settings page idle for %s, shutting the service down", settingsIdleTimeout)
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

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(webui.Assets()))))
	mux.HandleFunc("/api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/action", s.handleAction)
	return mux
}

// authorised accepts the token from the query string, which the browser uses
// on first load, or from a header, which the page uses afterwards. Requiring a
// custom header for writes also makes cross site requests impossible.
func (s *Server) authorised(r *http.Request) bool {
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

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "invalid or missing token", http.StatusForbidden)
		return
	}
	cfg := s.state.Config()
	lang := i18n.Lang(r.URL.Query().Get("lang"))
	if lang != i18n.LangZH && lang != i18n.LangEN {
		lang = i18n.CurrentLang()
	}
	writeJSON(w, map[string]any{
		"version":     Version,
		"repo":        Repo,
		"platform":    PlatformName(),
		"lang":        string(lang),
		"langSetting": cfg.Language,
		"i18n":        i18n.ALL(lang),
		"config":      cfg,
		"operators":   config.Operators,
		"status":      statusPayload(s.state.Snapshot()),
		"autostart":   platform.AutostartEnabled(),
		"configured":  cfg.Configured(),
		"paths": map[string]string{
			"config": cfg.Path(),
			"log":    logging.Log.Path(),
		},
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "invalid or missing token", http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]any{
		"status":    statusPayload(s.state.Snapshot()),
		"autostart": platform.AutostartEnabled(),
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorised(r) {
		http.Error(w, "invalid or missing token", http.StatusForbidden)
		return
	}

	// The request carries the whole configuration back, so it is decoded onto a
	// clone: a request that is refused below must not have written anything into
	// the configuration the program is running with.
	next := s.state.Config().Clone()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(next); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"ok":     false,
			"errors": map[string]string{"_": "err.badRequest"},
		})
		return
	}
	if errs := next.Validate(); len(errs) > 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "errors": errs})
		return
	}
	next.ProbeURLs = config.CompactProbes(next.ProbeURLs)
	if err := s.state.Update(next); err != nil {
		logging.Log.Error("saving settings from the page: %v", err)
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{
			"ok":     false,
			"errors": map[string]string{"_": "err.saveFailed"},
		})
		return
	}
	logging.Log.Info("settings page saved the config: %s", next.Summary())
	writeJSON(w, map[string]any{"ok": true, "config": next})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
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
		// Index names the entry of the login list an action is about, used by
		// "test this account".
		Index int `json:"index"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "err.badRequest"})
		return
	}

	switch req.Action {
	case "check":
		logging.Log.Info("manual check requested from the settings page")
		s.state.Trigger()
		writeJSON(w, map[string]any{"ok": true})

	case "testLogin":
		attempt, ok := s.attemptAt(req.Index)
		if !ok {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "err.noUsableEntry"})
			return
		}
		logging.Log.Info("login self test requested from the settings page: %s (entry %d)",
			attempt.Entry(), attempt.Index+1)
		test := s.state.TestLogin(attempt)
		writeJSON(w, map[string]any{
			"ok":     true,
			"index":  attempt.Index,
			"before": statusPayload(snapshotFromStatus(test.Before)),
			"result": test.Result,
			"after":  statusPayload(snapshotFromStatus(test.After)),
		})

	case "reload":
		next, err := s.state.Reload()
		if err != nil {
			logging.Log.Error("reloading config from the page: %v", err)
			writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "err.loadFailed"})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "config": next})

	case "openConfig":
		platform.OpenExternal(s.state.Config().Path())
		writeJSON(w, map[string]any{"ok": true})

	case "openLog":
		if p := logging.Log.Path(); p != "" {
			platform.OpenExternal(p)
		}
		writeJSON(w, map[string]any{"ok": true})

	case "logTail":
		writeJSON(w, map[string]any{"ok": true, "lines": tailLog(logTailLines)})

	case "autostart":
		logging.Log.Info("autostart changed from the settings page: %v", req.Value)
		ok := platform.SetAutostart(req.Value)
		writeJSON(w, map[string]any{"ok": ok, "autostart": platform.AutostartEnabled()})

	case "quit":
		logging.Log.Info("exit requested from the settings page")
		writeJSON(w, map[string]any{"ok": true})
		if s.onQuit != nil {
			go s.onQuit()
		}

	default:
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "err.unknownAction"})
	}
}

// attemptAt is the first attempt of the entry at index. An entry that is
// disabled or has no password is not one that can be submitted, and saying so is
// better than sending whatever happens to be in it.
func (s *Server) attemptAt(index int) (config.Attempt, bool) {
	for _, a := range s.state.Config().Attempts() {
		if a.Index == index {
			return a, true
		}
	}
	return config.Attempt{}, false
}

// tailLog returns the last n lines of the log file, oldest first.
func tailLog(n int) []string {
	path := logging.Log.Path()
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

// serveAsset writes one file of the embedded settings page. Caching is off so
// an updated page cannot be paired with an old executable's API.
func serveAsset(w http.ResponseWriter, r *http.Request, name, contentType string) {
	data, err := fs.ReadFile(webui.Assets(), name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
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
