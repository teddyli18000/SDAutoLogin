package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"slices"
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
	// settingsReadTimeout bounds reading a request. The forms the page sends are
	// small, so a caller that is still sending one after this long has stopped
	// meaning to.
	settingsReadTimeout = 30 * time.Second
	// settingsMinWriteTimeout is the shortest deadline a reply may have, for a
	// configuration that names no request timeout at all.
	settingsMinWriteTimeout = 30 * time.Second
	// settingsPortalRequests is how many requests to the portal the longest
	// handler makes: reading the session state is two of them, and the login test
	// reads it, submits one login and reads it again.
	settingsPortalRequests = 6
	logTailLines           = 200
	// sessionCookie is the cookie the page's session is carried in after the
	// address it was opened with has been exchanged for it.
	sessionCookie = "sdauth"
)

// writeTimeout is the deadline for writing one reply. It is counted from the
// configured request timeout rather than pinned, because a handler that talks to
// the portal waits that long on each request it makes: a fixed deadline would
// cut a reply off while the program was still waiting for the portal to answer.
func writeTimeout(cfg *config.Config) time.Duration {
	d := time.Duration(cfg.HTTPTimeoutSeconds) * settingsPortalRequests * time.Second
	if d < settingsMinWriteTimeout {
		return settingsMinWriteTimeout
	}
	return d
}

// contentSecurityPolicy lets the page load its own script, stylesheet and
// images and make its own requests, and nothing else. It has no inline script
// and no inline style, so neither has to be allowed for, which leaves an
// injected one nowhere to run.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Server serves the settings page. The address it is opened with carries a
// token, which is exchanged for a session cookie before the page is served, so
// a program that was not handed that address cannot read or change the
// configuration by accident, and the page does not keep the token in view.
type Server struct {
	state  *AppState
	onQuit func()
	// addr is where to listen, empty for loopback on a port the system picks.
	addr string

	mu    sync.Mutex
	srv   *http.Server
	url   string
	bound net.Addr
	// entry is the token the address handed to a browser carries. The browser
	// cannot put it in a header, since the first request is one it makes on its
	// own, so it arrives in the address and is exchanged for a cookie before the
	// page is served. Only that cookie is checked afterwards.
	entry string
	// session is the value of that cookie, and is made once per run. It is what a
	// request is checked against, so the token in an address is not the one the
	// page keeps using, and nothing the page is left showing carries it.
	session string
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

// SetOnQuit replaces the callback the page's exit button calls. A run that took
// over the server the tray had built gives it its own: the message the tray would
// have posted belongs to a window that never got a message loop.
func (s *Server) SetOnQuit(f func()) {
	s.mu.Lock()
	s.onQuit = f
	s.mu.Unlock()
}

// quitCallback is the callback above, read under the lock, so replacing it while
// a request is being served is safe.
func (s *Server) quitCallback() func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.onQuit
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

	entry, err := randomToken()
	if err != nil {
		return "", err
	}
	session, err := randomToken()
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
		ReadTimeout:       settingsReadTimeout,
		WriteTimeout:      writeTimeout(s.state.Config()),
		IdleTimeout:       2 * time.Minute,
	}
	s.entry, s.session = entry, session
	s.bound = listener.Addr()
	s.url = urlFor(s.bound, entry)
	s.srv = srv
	s.lastHit.Store(time.Now().UnixNano())

	if onDemand {
		// The tray opens the page when it is wanted, so the port is released
		// again once it has gone unused for a while. The address is written to
		// the log as it is handed out, since a new one is made for every opening.
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

	return s.url, nil
}

// rollEntry makes a new token for the address that is handed to a browser, so
// the one before it stops working: an address that was seen, written into the
// log or left in a browser's history opens the page once and no more. The
// address that is current goes to the log as it is made, which is where an
// operator with no tray reads it.
func (s *Server) rollEntry() (string, error) {
	entry, err := randomToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	s.entry = entry
	s.url = urlFor(s.bound, entry)
	url := s.url
	s.mu.Unlock()

	logging.Log.Info("settings page at %s", url)
	return url, nil
}

// urlFor builds the address to hand to a browser out of the address the server
// bound.
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
//
// Every opening gets an address of its own. The one that was handed out last
// time is spent by this, which is what makes two openings in a row work rather
// than sending the second browser to an address the first has already used.
func (s *Server) open(opener func(string) error) (string, error) {
	if _, err := s.URL(); err != nil {
		return "", err
	}
	url, err := s.rollEntry()
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
	s.srv, s.url, s.bound, s.entry, s.session = nil, "", nil, "", ""
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

// routes wires the handlers and puts the security headers in front of them.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(webui.Assets()))))
	mux.HandleFunc("/api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/action", s.handleAction)
	return s.withHeaders(mux)
}

// withHeaders puts the response headers on before the handler runs, on every
// answer including the refusals, since a caller without the token reads one of
// those. The page is opened with the token in its address, so it must not let
// that address travel out in a Referer and must not be framed by another page;
// what it may load is named rather than left to the page's own markup.
func (s *Server) withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// authorised reports whether a request may go through. The session cookie is
// what authenticates it, and a request that changes something has to carry the
// same value in a header as well: a page from another site cannot set a custom
// header without this server agreeing to a cross site request, and it does not.
func (s *Server) authorised(r *http.Request) bool {
	s.lastHit.Store(time.Now().UnixNano())
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		// The three ways a request is refused are told apart here, because which
		// one it was is the difference between a page left over from an earlier
		// run and a page that is not sending what this server asks of it.
		logging.Log.Debug("refusing %s %s: no session cookie, so the page was not opened from an address of this run",
			r.Method, r.URL.Path)
		return false
	}
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if !tokenEqual(cookie.Value, session) {
		logging.Log.Debug("refusing %s %s: the session cookie is not the one this run handed out",
			r.Method, r.URL.Path)
		return false
	}
	if r.Method == http.MethodGet {
		return true
	}
	if !tokenEqual(r.Header.Get("X-Auth-Token"), session) {
		logging.Log.Debug("refusing %s %s: the cookie was sent without the token header a change has to carry",
			r.Method, r.URL.Path)
		return false
	}
	return true
}

// tokenEqual compares two tokens in a time that does not depend on how many
// bytes of one match the other, so the token cannot be read out a byte at a
// time by timing the answers. A token that was never made matches nothing, not
// even another empty string, which the comparison on its own would call a
// match. A length that does not match is answered at once, which gives nothing
// away: every token this program makes is the same length.
func tokenEqual(got, want string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// The address the page was opened with carries a token, because the first
	// request of a navigation is one the browser makes on its own and cannot put
	// anything in a header. That token buys the session cookie here, and the
	// browser is sent to the same page without it: what it is left showing, its
	// history and the Referer of anything it links to carry no token at all.
	s.mu.Lock()
	entry := s.entry
	s.mu.Unlock()
	if tokenEqual(r.URL.Query().Get("token"), entry) {
		s.startSession(w)
		return
	}
	if !s.authorised(r) {
		http.Error(w, "invalid or missing token", http.StatusForbidden)
		return
	}
	serveAsset(w, r, "index.html", "text/html; charset=utf-8")
}

// startSession hands the browser the cookie the rest of the session is checked
// against, and sends it to the page again without the token it arrived with.
//
// The address that was used is spent here, and the next one is made and written
// to the log, so the page can always be opened again afterwards: from the tray,
// or, on a machine that serves the page for remote management and has none, by
// reading the newest address out of the log.
func (s *Server) startSession(w http.ResponseWriter) {
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if _, err := s.rollEntry(); err != nil {
		// The address that was used stays valid in that case. Saying so is
		// better than a page that cannot be opened again at all.
		logging.Log.Warn("making the next settings address: %v", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    session,
		Path:     "/",
		SameSite: http.SameSiteStrictMode,
		// Not Secure, and not HttpOnly: the server speaks plain http on a
		// loopback address, so a browser would drop a Secure cookie, and the page
		// reads this one to repeat it in the header of the requests it makes.
	})
	w.Header().Set("Location", "/")
	w.WriteHeader(http.StatusSeeOther)
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
		"version":     FullVersion(),
		"repo":        Repo,
		"platform":    PlatformName(),
		"lang":        string(lang),
		"langSetting": cfg.Language,
		"i18n":        i18n.ALL(lang),
		"config":      pageConfig(cfg),
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

// pageConfig is the configuration as the settings page may see it: each stored
// password is replaced by the fact that there is one.
//
// The page writes that field and never reads it, so there is no reason for the
// secret to leave the process. A password that is never sent cannot be read back
// out of the browser, out of whatever script the page loads, or out of a request
// the page makes. What it costs is that an entry coming back with an empty
// password has to be filled in from the stored one, which KeepPasswordsFrom does.
//
// Every answer that carries the configuration back to the page goes through
// here, the reload included: that one reads the file again, and what comes out
// of it is still the file's contents, so it is masked in the same way.
func pageConfig(cfg *config.Config) *config.Config {
	out := cfg.Clone()
	for i := range out.Accounts {
		// "There is a password" means the file holds one, not that this run has
		// one in memory: with the passwords turned off there is nothing stored
		// for an empty field to keep, and the page saying otherwise would be a
		// promise the next start does not keep.
		out.Accounts[i].HasPassword = out.Accounts[i].Password != "" && out.RememberPassword
		out.Accounts[i].Password = ""
	}
	return out
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
		logging.Log.Warn("the settings page sent a configuration that could not be read: %v", err)
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"ok":     false,
			"errors": map[string]string{"_": "err.badRequest"},
		})
		return
	}
	// The page is not given the stored passwords, so an account it sends back
	// with that field empty means "keep the one that is there". A password the
	// reader typed is in the request and is left alone.
	next.KeepPasswordsFrom(s.state.Config())
	if errs := next.Validate(); len(errs) > 0 {
		// The field paths of the refusal go to the log, since the page shows them
		// next to the inputs a reader may not be looking at: accounts.0.password
		// says a password is missing, never what it is.
		logging.Log.Warn("the settings page's configuration was refused: %s", describeErrors(errs))
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
	writeJSON(w, map[string]any{"ok": true, "config": pageConfig(next)})
}

// describeErrors is a validation result as one line: the field paths and the
// message keys that go with them, in a stable order, so a refusal in the log can
// be read against the page. The paths name the fields, never their values.
func describeErrors(errs map[string]string) string {
	fields := make([]string, 0, len(errs))
	for field := range errs {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, field+"="+errs[field])
	}
	return strings.Join(parts, "; ")
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
		// ISP is the service type a switch asks for, as the suffix the entry is
		// submitted with. It is a pointer because the campus network itself is a
		// service type with no suffix: a request that names none at all is a
		// malformed one, not a request for the campus network.
		ISP *string `json:"isp"`
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
			"ok":            true,
			"index":         attempt.Index,
			"alreadyOnline": test.AlreadyOnline,
			"before":        statusPayload(snapshotFromStatus(test.Before)),
			"result":        test.Result,
			"after":         statusPayload(snapshotFromStatus(test.After)),
		})

	case "switchService":
		// The controls beside the login list, for a session that is up on a
		// service type other than the one that was asked for. It is the same
		// answer the page shows for a login of its own, since that is what this
		// is.
		if req.ISP == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "err.badRequest"})
			return
		}
		logging.Log.Info("the settings page asked for the session to be signed in as %s", config.OperatorName(*req.ISP))
		test, moved := s.state.SwitchService(*req.ISP)
		if moved {
			logging.Log.Info("signed in again as %s: %s (ret_code=%s)", test.Attempt.Entry(),
				test.Result.Message, test.Result.RetCode)
		} else {
			logging.Log.Info("there was nothing to sign in again as %s: the session is not up on this account, or it is on that service type already",
				config.OperatorName(*req.ISP))
		}
		writeJSON(w, map[string]any{
			"ok":     true,
			"moved":  moved,
			"index":  test.Attempt.Index,
			"isp":    test.Attempt.ISP,
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
		writeJSON(w, map[string]any{"ok": true, "config": pageConfig(next)})

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
		if f := s.quitCallback(); f != nil {
			go f()
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
