package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
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
	// small, so a caller still sending one after this long has stopped meaning to.
	settingsReadTimeout = 30 * time.Second
	// settingsMinWriteTimeout is the shortest deadline a reply may have, for a
	// configuration that names no request timeout.
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

// writeTimeout is the deadline for writing one reply, counted from the configured
// request timeout: a handler that talks to the portal waits that long per request,
// so a fixed deadline would cut it off.
//
// It is worked out again for every request rather than fixed when the server
// starts: the request timeout is a setting on the page, and a deadline taken from
// the configuration of the day would cut off a reply the reader had just asked to
// be allowed to wait for. See withWriteDeadline.
func writeTimeout(cfg *config.Config) time.Duration {
	d := time.Duration(cfg.HTTPTimeoutSeconds) * settingsPortalRequests * time.Second
	if d < settingsMinWriteTimeout {
		return settingsMinWriteTimeout
	}
	return d
}

// contentSecurityPolicy lets the page load only its own script, stylesheet and
// images and make its own requests. It allows no inline script or style, which
// leaves an injected one nowhere to run.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Server serves the settings page. The address it is opened with carries a token,
// exchanged for a session cookie before the page is served, so a program not
// handed that address cannot read or change the configuration.
type Server struct {
	state  *AppState
	onQuit func()
	// addr is where to listen, empty for loopback on a port the system picks.
	addr string
	// openExternal hands a file or an address to the desktop for the actions that
	// open something: the configuration file, the log, the portal page. It is a
	// field so a test can read what would be opened without starting a browser.
	openExternal func(string) error

	mu    sync.Mutex
	srv   *http.Server
	url   string
	bound net.Addr
	// entry is the token the address handed to a browser carries. The browser cannot
	// put it in a header, since the first request is one it makes on its own, so it
	// arrives in the address and is exchanged for a cookie before the page is served.
	entry string
	// session is the value of that cookie, made once per run. It is what a request
	// is checked against, so the token in an address is not the one the page keeps
	// using, and nothing the page is left showing carries it.
	session string
	lastHit atomic.Int64
}

// NewServer returns a settings server for state. An empty addr means loopback on a
// port the system picks, up on first use and shut down once idle; a named address
// starts at once and stays up. onQuit runs after the page asks the program to exit.
func NewServer(state *AppState, addr string, onQuit func()) *Server {
	return &Server{state: state, addr: addr, onQuit: onQuit, openExternal: platform.OpenExternal}
}

// SetOnQuit replaces the callback the page's exit button calls. A run that took
// over the tray's server gives it its own, since the tray's message belongs to a
// window that never got a message loop.
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

// Addr is the address the settings page is served on, without the token in it.
// It is what a log line may carry: the token lives in the address handed to a
// browser, and nothing else may hold it. Empty until the server is started,
// and false when it is serving on no address at all.
func (s *Server) Addr() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.url == "" {
		return "", false
	}
	if i := strings.LastIndex(s.url, "/?"); i > 0 {
		return s.url[:i], true
	}
	return s.url, true
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
		// again once it has gone unused for a while. The address is handed to
		// the browser that opens it and is not written down here: the log
		// outlives the token, and a token kept where logs are collected and
		// copied is a way in that outlives its one opening.
		go s.reapWhenIdle()
	} else {
		if host, _, err := net.SplitHostPort(listener.Addr().String()); err == nil && !isLoopback(host) {
			logging.Log.Warn("the settings page is reachable from the network on %s; the token in its address is the only protection, so keep it on a trusted network or behind a tunnel", listener.Addr())
		}
		// Nothing opens this one by itself: it is meant for a machine with no
		// tray, so the address goes to the console, which is where the person who
		// asked for the page is reading. The log file would keep the token past
		// its one opening and travel with every log that is copied or collected.
		logging.Log.Info("settings page for remote management on %s, staying up until the program stops", s.bound.String())
		fmt.Fprintf(os.Stderr, "settings page for remote management: open %s (good for one opening; it is replaced after each one)\n", s.url)
	}

	// The local srv is what this goroutine serves on: Stop clears the field, so
	// reading it from here would race with a shutdown right after the start.
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			logging.Log.Error("settings service stopped: %v", err)
		}
	}()

	return s.url, nil
}

// rollEntry makes a new token for the address handed to a browser, so the one
// before it stops working: an address that was seen, logged or left in a
// history opens the page once and no more. The new address goes to the log.
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

	// The address carries the one time token and the log outlives it, so the
	// port is what is written down. A reader who needs the address is handed it
	// where it is used: by the tray, or by the browser this run opens.
	logging.Log.Info("settings page on %s", s.bound.String())
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

// hostForURL picks the host to put in that address. A listener on every interface
// has no useful host of its own, so the loopback address is used; an operator
// reaching the page from elsewhere replaces it with this machine's address.
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

// open serves the page and hands the address to opener, and every opening gets an
// address of its own: this spends the one handed out last time. The address comes
// back even when opening it failed, so the caller can show it to the user.
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
	mux.Handle("/static/", s.assets())
	mux.HandleFunc("/api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/action", s.handleAction)
	// The deadline is set outside the headers, so that every answer carries one:
	// a refusal and a missing address are written by the same connections as a
	// page, and a caller that stops reading holds one of them just as long. The
	// host is checked inside them, so a name this server does not answer to is
	// refused before any handler sees the request.
	return s.withWriteDeadline(s.withHeaders(s.withHost(mux)))
}

// withWriteDeadline gives every reply the deadline the configuration in force
// asks for, counted from the moment the request arrived.
//
// The deadline is set here rather than left to the server's WriteTimeout because
// that one is read when the server starts. The request timeout can be changed
// from the page, and a copy that kept the deadline of its first configuration
// would either cut off a reply it had just been told to wait for, or hold a
// connection open for a wait nobody asked for any more.
func (s *Server) withWriteDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The controller is how a handler reaches the connection's deadline: the
		// interface it is given does not carry one. A connection that cannot take
		// one is one that is already gone, and answering it is the caller's
		// business rather than a reason to refuse here.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeTimeout(s.state.Config()))); err != nil {
			logging.Log.Debug("no reply deadline for %s %s: %v", r.Method, r.URL.Path, err)
		}
		next.ServeHTTP(w, r)
	})
}

// assets serves the page's own files: its script, its stylesheet and the mark.
// They are served through the same check as everything else, so the session the
// address was exchanged for is what reads them. The files hold no secret, but
// they are the page, and a page served to a program that never opened the
// address is one it can read, copy or hang a frame around. A browser has the
// cookie by the time it asks for them: the markup that names them is served
// after the exchange, on the same origin.
func (s *Server) assets() http.Handler {
	files := http.StripPrefix("/static/", http.FileServer(http.FS(webui.Assets())))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorised(r) {
			http.Error(w, "invalid or missing token", http.StatusForbidden)
			return
		}
		// The files are part of the executable, so nothing may be kept: a page from
		// a cache would be paired with the API of a different build. What the markup
		// is served with says the same, and this is the way the script and the
		// stylesheet are read.
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
}

// withHeaders puts the headers on before the handler runs, refusals included: a
// caller without the token reads one of those. The headers keep the tokenised
// address out of a Referer and the page unframed; the policy names what it loads.
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

// withHost refuses a request whose Host header does not name this machine, so that a
// page served from another name — one a browser was made to resolve to 127.0.0.1 —
// cannot reach this server as an origin of its own. Without it the protection is
// only what the browser does with a cookie, which is a property of the page and not
// of this server.
func (s *Server) withHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			logging.Log.Debug("refusing %s %s: the Host header %q does not name this machine", r.Method, r.URL.Path, r.Host)
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowed reports whether a request may be answered under the Host header it
// carries: the loopback address this page is served on, the address the listener was
// bound to, or — for a listener on every interface — an address written as an IP
// literal, which is what an operator reaching the page from another machine sends and
// what a name a rebinding page owns is not.
func (s *Server) hostAllowed(hostPort string) bool {
	host := hostName(hostPort)
	if host == "" {
		// A browser always sends one, so a request without one is not the page.
		return false
	}
	if isLoopback(host) {
		return true
	}
	s.mu.Lock()
	bound := s.bound
	s.mu.Unlock()
	if bound == nil {
		// No listener: no request can arrive over a network, and the handler chain is
		// being driven directly.
		return true
	}
	boundHost := hostName(bound.String())
	if strings.EqualFold(boundHost, host) {
		return true
	}
	if isWildcardHost(boundHost) {
		return net.ParseIP(host) != nil
	}
	return false
}

// hostName is the host part of a Host header or of a listener address, with the port
// and the brackets an IPv6 literal is written with taken off.
func hostName(hostPort string) string {
	host := strings.TrimSpace(hostPort)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
}

// isWildcardHost reports whether a listener address names every interface.
func isWildcardHost(host string) bool {
	switch host {
	case "", "0.0.0.0", "::":
		return true
	}
	return false
}

// authorised reports whether a request may go through. The session cookie
// authenticates it; a write also carries the value in a header, which a page from
// another site cannot set without this server agreeing to a cross site request.
func (s *Server) authorised(r *http.Request) bool {
	s.lastHit.Store(time.Now().UnixNano())
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		// The three ways a request is refused are told apart here: which one it was
		// distinguishes a page left from an earlier run from one not sending the header.
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

// tokenEqual compares tokens in constant time, so the token cannot be read off the
// reply time. A token that was never made matches nothing, empty strings included.
// A length mismatch is answered at once, since every token is the same length.
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
	// The address the page was opened with carries a token, because the first request
	// of a navigation is one the browser makes on its own and cannot put anything in a
	// header. That token buys the session cookie here, and the browser is sent on
	// without it, so the address bar and the history carry no token.
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
// against, and sends it back to the page without the token it arrived with: the
// used address is spent and the next one made, so the page can be opened again.
func (s *Server) startSession(w http.ResponseWriter) {
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if _, err := s.rollEntry(); err != nil {
		// The address that was used stays valid in that case, and saying so is
		// better than a page that cannot be opened again.
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
		"configured":  cfg.HasAccounts(),
		"paths": map[string]string{
			"config": cfg.Path(),
			"log":    logging.Log.Path(),
		},
	})
}

// pageConfig masks the configuration for the settings page: each stored password
// is replaced by where it comes from, so the secret stays in the process. Every
// answer that carries it to the page comes through here, the reload included.
func pageConfig(cfg *config.Config) *config.Config {
	out := cfg.Clone()
	for i := range out.Accounts {
		// A password in memory with no source recorded came from this file.
		source := out.Accounts[i].PasswordSource
		if source == "" && out.Accounts[i].Password != "" {
			source = config.SourceConfig
		}
		// One in this file does not survive a save with the passwords turned off,
		// and the page saying it is there is a promise the next start breaks. One
		// read from a password file is kept, since that switch does not govern it.
		if !out.RememberPassword && source == config.SourceConfig {
			source = ""
		}
		out.Accounts[i].PasswordSource = source
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
	// copy: a request refused below must not have written into the running
	// configuration. What came from a password file is taken out of that copy, so
	// that a password the reader did not type can be told from one they did.
	// A page from another site can post the three content types a form is allowed to
	// use without asking this server first, and none of them is this one. A request
	// that names no type at all is taken: a form cannot make one, and the page always
	// says what it sends.
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
			logging.Log.Warn("the settings page sent a configuration as %q, which is not JSON", contentType)
			writeJSONStatus(w, http.StatusUnsupportedMediaType, map[string]any{
				"ok":     false,
				"errors": map[string]string{"_": "err.badRequest"},
			})
			return
		}
	}

	next := s.state.Config().PageBase()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	// A field this program does not know is a request that does not match this
	// version of the page, and refusing it keeps one from naming a setting this
	// program keeps for itself.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(next); err != nil {
		logging.Log.Warn("the settings page sent a configuration that could not be read: %v", err)
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"ok":     false,
			"errors": map[string]string{"_": "err.badRequest"},
		})
		return
	}
	// Where a password came from is this program's own word about an entry, never the
	// request's, so every source a request carried is cleared here. What the page did
	// not type is filled in again from the configuration that is running by
	// ReconcilePasswords, which puts the label that entry had back on with it.
	for i := range next.Accounts {
		next.Accounts[i].PasswordSource = ""
	}
	// The page is never given a stored password, so an account sent back with that
	// field empty means keep the one that is there, and a typed password wins. The
	// file may have been changed by the same request, so where each password comes
	// from is worked out again from what the request now names.
	if notice := next.ReconcilePasswords(s.state.Config()); notice != "" {
		logging.Log.Warn("%s", notice)
	}
	if errs := next.Validate(); len(errs) > 0 {
		// The field paths of the refusal go to the log, since the page shows them next
		// to the inputs: accounts.0.password says a password is missing, and no value
		// of that field is written with it.
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
// be read against the page. The paths name the fields and never their values.
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
		// service type with no suffix, and a request that names none is malformed.
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
		// The controls beside the login list, for a session up on a service type other
		// than the one asked for. It is answered like a login of the page's own, since
		// that is what it is.
		if req.ISP == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "err.badRequest"})
			return
		}
		logging.Log.Info("the settings page asked for the session to be signed in as %s", config.OperatorName(*req.ISP))
		test, moved := s.state.SwitchService(*req.ISP)
		// Nothing is logged for the round that moved nothing: SwitchService names the
		// reason, and a second line here would only be a vaguer copy of it.
		if moved {
			logging.Log.Info("the page is shown what came back from signing in again as %s: %s (ret_code=%s)",
				test.Attempt.Entry(), test.Result.Message, test.Result.RetCode)
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
		s.openExternal(s.state.Config().Path())
		writeJSON(w, map[string]any{"ok": true})

	case "openLog":
		if p := logging.Log.Path(); p != "" {
			s.openExternal(p)
		}
		writeJSON(w, map[string]any{"ok": true})

	case "openPortal":
		// The way out of a session the portal will not sign in again: the reader signs
		// it out on the portal's own page and asks for the switch afterwards. The
		// address opened is the configured one, so no request can choose it.
		url := s.state.Config().PortalBaseURL
		logging.Log.Info("the settings page asked for the portal page at %s", url)
		if err := s.openExternal(url); err != nil {
			logging.Log.Warn("opening the portal page %s: %v", url, err)
			writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "err.openPortal"})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "url": url})

	case "checkUpdate":
		// The reader asking, so it runs whether or not the automatic check is on.
		logging.Log.Info("a check for a newer release was requested from the settings page")
		if !s.state.CheckForUpdateNow() {
			writeJSONStatus(w, http.StatusServiceUnavailable,
				map[string]any{"ok": false, "error": "err.noUpdater"})
			return
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
