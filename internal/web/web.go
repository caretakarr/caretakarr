// Package web is the server-rendered admin UI (html/template + htmx).
package web

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/caretakarr/caretakarr/internal/caddylog"
	"github.com/caretakarr/caretakarr/internal/cfdns"
	"github.com/caretakarr/caretakarr/internal/config"
	"github.com/caretakarr/caretakarr/internal/discovery"
	"github.com/caretakarr/caretakarr/internal/dnssync"
	"github.com/caretakarr/caretakarr/internal/probe"
	"github.com/caretakarr/caretakarr/internal/store"
)

//go:embed templates static
var assets embed.FS

// Applier is the apply service: every config change goes through Mutate.
type Applier interface {
	Mutate(ctx context.Context, fn func(tx *store.Store) error) error
	StartupError() error
}

type Deps struct {
	Cfg   config.Config
	Store *store.Store
	Apply Applier
	CF    *cfdns.Client
	DNS   *dnssync.Syncer
	Probe *probe.Monitor
	// Docker is optional; the discovery panel shows only when its socket is mounted.
	Docker *discovery.Client
	Logs   *caddylog.Ring
}

type Server struct {
	deps       Deps
	pages      map[string]*template.Template
	auth       *authn
	startupErr func() error
	cfErr      atomic.Value // string; set by VerifyToken

	flashMu sync.Mutex
	flashes []string // one-time notices shown on the next page render
}

func (s *Server) flash(msg string) {
	s.flashMu.Lock()
	defer s.flashMu.Unlock()
	s.flashes = append(s.flashes, msg)
}

func (s *Server) takeFlashes() []string {
	s.flashMu.Lock()
	defer s.flashMu.Unlock()
	f := s.flashes
	s.flashes = nil
	return f
}

func New(d Deps) *Server {
	s := &Server{deps: d, pages: map[string]*template.Template{}, auth: newAuthn(d.Cfg.AdminPassword)}
	for _, p := range []string{"dashboard", "login", "login_totp", "security", "settings", "domains", "route", "site", "logs", "help"} {
		s.pages[p] = template.Must(template.New(p).Funcs(helpFuncs).ParseFS(assets, "templates/layout.html", "templates/docker.html", "templates/"+p+".html"))
	}
	if d.Apply != nil {
		s.startupErr = d.Apply.StartupError
	}
	if d.Cfg.AdminPassword == "" {
		slog.Warn("ADMIN_PASSWORD is not set: the web UI is open to anyone who can reach it")
		if d.Cfg.TurnstileSecret != "" || d.Cfg.SecretKey != "" {
			slog.Warn("2FA, passkeys and Turnstile are inactive while ADMIN_PASSWORD is unset")
		}
	}
	s.warnUnreadableSeed()
	return s
}

// VerifyToken checks the Cloudflare token once and records a failure for the UI banner.
func (s *Server) VerifyToken(ctx context.Context) {
	if err := s.deps.CF.VerifyToken(ctx); err != nil {
		slog.Error("cloudflare token check failed", "error", err)
		s.cfErr.Store(err.Error())
		return
	}
	s.cfErr.Store("")
}

func (s *Server) Handler() http.Handler {
	static, _ := fs.Sub(assets, "static")
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginSubmit)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /login/totp", s.loginTOTPPage)
	mux.HandleFunc("POST /login/totp", s.loginTOTPSubmit)
	mux.HandleFunc("POST /login/passkey/begin", s.passkeyLoginBegin)
	mux.HandleFunc("POST /login/passkey/finish", s.passkeyLoginFinish)
	mux.Handle("GET /security", s.security(s.securityPage, true))
	mux.Handle("GET /settings", s.protected(s.settingsPage))
	mux.Handle("POST /settings/smtp", s.security(s.saveSMTP, false))
	mux.Handle("POST /security/totp/begin", s.security(s.totpBegin, true))
	mux.Handle("POST /security/totp/confirm", s.security(s.totpConfirm, true))
	mux.Handle("POST /security/totp/disable", s.security(s.totpDisable, false))
	mux.Handle("POST /security/backup-codes", s.security(s.backupRegenerate, false))
	mux.Handle("POST /security/passkeys/begin", s.security(s.passkeyRegisterBegin, false))
	mux.Handle("POST /security/passkeys/finish", s.security(s.passkeyRegisterFinish, false))
	mux.Handle("DELETE /security/passkeys/{id}", s.security(s.passkeyDelete, false))
	mux.Handle("POST /security/gate/revoke", s.security(s.revokeGateIdentity, false))
	mux.Handle("GET /{$}", s.protected(s.dashboard))
	mux.Handle("GET /help", s.protected(s.helpPage))
	mux.Handle("GET /status", s.protected(s.status))
	mux.Handle("GET /logs", s.protected(s.logsPage))
	mux.Handle("GET /logs/rows", s.protected(s.logRows))
	mux.Handle("GET /domains", s.protected(s.domainsPage))
	mux.Handle("POST /domains", s.protected(s.addDomain))
	mux.Handle("DELETE /domains/{id}", s.protected(s.deleteDomain))
	mux.Handle("GET /sites/new", s.protected(s.newSitePage))
	mux.Handle("POST /sites", s.protected(s.addSite))
	mux.Handle("GET /routes/new", s.protected(s.newRoutePage))
	mux.Handle("POST /routes", s.protected(s.createRoute))
	mux.Handle("GET /routes/{id}", s.protected(s.editRoutePage))
	mux.Handle("PUT /routes/{id}", s.protected(s.updateRoute))
	mux.Handle("DELETE /routes/{id}", s.protected(s.deleteRoute))
	mux.Handle("POST /host/dns/resync", s.protected(s.hostDNS(false)))
	mux.Handle("POST /host/dns/takeover", s.protected(s.hostDNS(true)))
	mux.Handle("POST /routes/{id}/toggle", s.protected(s.toggleRoute))
	mux.Handle("POST /routes/{id}/dns/resync", s.protected(s.resyncDNS))
	mux.Handle("POST /routes/{id}/dns/takeover", s.protected(s.takeOverDNS))
	return s.csrf(mux)
}

type page struct {
	Title      string
	Open       bool // no ADMIN_PASSWORD: UI is unauthenticated
	StartupErr string
	CFErr      string
	Flash      []string
	LoggedIn   bool
	Data       any
}

func (s *Server) render(w http.ResponseWriter, name string, status int, p page) {
	p.Open = !s.auth.enabled()
	p.LoggedIn = s.auth.enabled()
	p.Flash = s.takeFlashes()
	if s.startupErr != nil {
		if err := s.startupErr(); err != nil {
			p.StartupErr = err.Error()
		}
	}
	if v, ok := s.cfErr.Load().(string); ok {
		p.CFErr = v
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.pages[name].ExecuteTemplate(w, "layout", p); err != nil {
		slog.Error("render page", "page", name, "error", err)
	}
}

// status renders just the routes table; the dashboard polls it every 10s.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	routes, err := s.routeRows(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages["dashboard"].ExecuteTemplate(w, "routes", routes); err != nil {
		slog.Error("render status", "error", err)
	}
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	domains, err := s.domainRows(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	routes, err := s.routeRows(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	host := s.hostCard(r.Context())
	items := s.checklist(r.Context(), routes, host)
	s.render(w, "dashboard", http.StatusOK, page{Title: "Dashboard", Data: struct {
		Domains   []domainRow
		Routes    []routeRow
		Host      *hostCard
		Checklist []checkItem
		SetupDone bool
	}{domains, routes, host, items, setupDone(items)}})
}
