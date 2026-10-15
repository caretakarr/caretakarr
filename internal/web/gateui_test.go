package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/caretakarr/caretakarr/internal/config"
	"github.com/caretakarr/caretakarr/internal/store"
)

// gateSettings is the settings row the gate tests need: mail ready.
func gateSettings() store.Settings {
	return store.Settings{SMTPHost: "127.0.0.1", SMTPPort: 28097, SMTPUser: "u",
		SMTPPass: []byte{1}, MailFrom: "access@example.com", UpdatedAt: "2026-10-15T10:00:00Z"}
}

// gateCfg is config with the admin password and session key the gate needs.
func gateCfg() config.Config {
	return config.Config{AdminPassword: "pw", SecretKey: "e2e-secret-key-0123456789abcdef-e2e"}
}

// gateServer is domainServer's wiring with gate settings and the gate's config.
func gateServer(t *testing.T) *Server {
	cf := &cfFake{zones: zonesOK}
	s := cfgServer(t, cf, gateCfg()) // gateCfg: AdminPassword + SecretKey set
	if err := s.deps.Store.SaveSettings(context.Background(), gateSettings()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseGate(t *testing.T) {
	ctx := context.Background()
	v := url.Values{"gate_acl": {"alice@example.com\n@dev.example"}}
	s := gateServer(t)
	gs, err := s.parseGate(ctx, store.Route{GateMode: "acl"}, v)
	if err != nil || len(gs) != 2 || gs[0].Mode != "email" || gs[1].Mode != "domain" {
		t.Fatalf("acl: %v %v", gs, err)
	}
	if gs, err = s.parseGate(ctx, store.Route{GateMode: "open"}, url.Values{}); err != nil || len(gs) != 0 {
		t.Fatalf("open: %v %v", gs, err)
	}
	if gs, err = s.parseGate(ctx, store.Route{GateMode: ""}, v); err != nil || len(gs) != 0 {
		t.Fatalf("none: %v %v", gs, err)
	}
	// No secret key: saving a gated route is refused naming it (mail IS ready here).
	s2 := newServer(t, "pw")
	if err := s2.deps.Store.SaveSettings(ctx, gateSettings()); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.parseGate(ctx, store.Route{GateMode: "acl"}, v); err == nil || !strings.Contains(err.Error(), "SECRET_KEY") {
		t.Fatalf("secret-key refusal: %v", err)
	}
	// Mail not ready: refused pointing at Settings.
	s3 := newServer(t, "pw")
	s3.deps.Cfg = config.Config{SecretKey: "e2e-secret-key-0123456789abcdef-e2e"}
	if _, err := s3.parseGate(ctx, store.Route{GateMode: "acl"}, v); err == nil || !strings.Contains(err.Error(), "Settings") {
		t.Fatalf("mail refusal: %v", err)
	}
}

func TestRouteFormShowsGateSection(t *testing.T) {
	s := gateServer(t)
	if _, err := s.deps.Store.CreateDomain(context.Background(), store.Domain{Name: "gateui.example.net", DefaultIP: "192.168.1.5", CertMode: "wildcard"}); err != nil {
		t.Fatal(err)
	}
	c := sessionCookie(t, login(s, "pw", nil, ""))
	w := s.do(req{path: "/routes/new", cookies: []*http.Cookie{c}})
	if !strings.Contains(w.Body.String(), "gate_mode") || !strings.Contains(w.Body.String(), "Magic-link access") {
		t.Fatalf("gate section missing from the route form:\n%s", w.Body.String())
	}
	// Mail unconfigured: the section stays visible but disabled, with the note.
	s2 := cfgServer(t, &cfFake{zones: zonesOK}, config.Config{AdminPassword: "pw"})
	if _, err := s2.deps.Store.CreateDomain(context.Background(), store.Domain{Name: "gateui2.example.net", DefaultIP: "192.168.1.6", CertMode: "wildcard"}); err != nil {
		t.Fatal(err)
	}
	c2 := sessionCookie(t, login(s2, "pw", nil, ""))
	w2 := s2.do(req{path: "/routes/new", cookies: []*http.Cookie{c2}})
	if strings.Contains(w2.Body.String(), "gate_mode") {
		t.Fatal("controls rendered while mail is unconfigured")
	}
	if !strings.Contains(w2.Body.String(), "unlock afterwards") {
		t.Fatalf("disabled note missing: %s", w2.Body.String())
	}
}

func TestCreateGatedRouteAndSecurityListing(t *testing.T) {
	s := gateServer(t) // Apply = txApplier, DNS = cftest fake, auth = "pw"
	if _, err := s.deps.Store.CreateDomain(context.Background(), store.Domain{Name: "example.com", DefaultIP: "192.168.1.5", CertMode: "wildcard"}); err != nil {
		t.Fatal(err)
	}
	c := sessionCookie(t, login(s, "pw", nil, ""))
	form := url.Values{
		"domain_id":     {"1"},
		"subdomain":     {"st"},
		"scheme":        {"http"},
		"upstream_host": {"192.168.1.20"},
		"upstream_port": {"8989"},
		"gate_mode":     {"acl"},
		"gate_acl":      {"alice@example.com\n@dev.example"},
	}
	w := s.do(req{method: "POST", path: "/routes", body: form.Encode(), hdr: htmxPost, cookies: []*http.Cookie{c}})
	if w.Code != 204 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	rs, err := s.deps.Store.ListRoutes(context.Background())
	if err != nil || len(rs) != 1 || rs[0].GateMode != "acl" {
		t.Fatalf("route: %+v %v", rs, err)
	}
	gs, _ := s.deps.Store.Grants(context.Background(), rs[0].ID)
	if len(gs) != 2 {
		t.Fatalf("grants saved: %+v", gs)
	}
	if err := s.deps.Store.UpsertIdentity(context.Background(), "alice@example.com", "n1", "hash1", "2026-10-15T10:00:00Z", 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	w = s.do(req{path: "/security", cookies: []*http.Cookie{c}})
	if !strings.Contains(w.Body.String(), "alice@example.com") || !strings.Contains(w.Body.String(), "Site access sessions") {
		t.Fatalf("security page: %s", w.Body.String())
	}
	w2 := s.do(req{method: "POST", path: "/security/gate/revoke", body: "email=alice@example.com", hdr: htmxPost, cookies: []*http.Cookie{c}})
	if w2.Code != 204 {
		t.Fatalf("revoke: %d %s", w2.Code, w2.Body.String())
	}
	if gs, _ := s.deps.Store.GateIdentities(context.Background()); len(gs) != 0 {
		t.Fatalf("identity survived revoke: %+v", gs)
	}
}

// TestSettingsPageEndToEnd covers Stage 2: page render, bad save, good save, gate enable.
func TestSettingsPageEndToEnd(t *testing.T) {
	s := gateServer(t) // settings row already prefilled
	c := sessionCookie(t, login(s, "pw", nil, ""))

	// Page renders the current state.
	w := s.do(req{path: "/settings", cookies: []*http.Cookie{c}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Email delivery") {
		t.Fatalf("settings page: %d %s", w.Code, w.Body.String())
	}

	// Malformed sender is refused.
	w = s.do(req{method: "POST", path: "/settings/smtp",
		body: url.Values{"smtp_host": {"mail.x"}, "mail_from": {"not-an-email"}}.Encode(), hdr: htmxPost, cookies: []*http.Cookie{c}})
	if w.Code != 422 || !strings.Contains(w.Body.String(), "bare") {
		t.Fatalf("bad from: %d %s", w.Code, w.Body.String())
	}

	// One of host/from: refused.
	w = s.do(req{method: "POST", path: "/settings/smtp",
		body: url.Values{"smtp_host": {"mail.x"}}.Encode(), hdr: htmxPost, cookies: []*http.Cookie{c}})
	if w.Code != 422 {
		t.Fatalf("half config: %d", w.Code)
	}

	// Good save: ready, and the route forms' gate section turns on.
	w = s.do(req{method: "POST", path: "/settings/smtp",
		body: url.Values{"smtp_host": {"mail.x"}, "smtp_port": {"587"}, "mail_from": {"access@x.io"}}.Encode(),
		hdr:  htmxPost, cookies: []*http.Cookie{c}})
	if w.Code != 204 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	set, err := s.deps.Store.GetSettings(context.Background())
	if err != nil || !set.MailReady() || set.SMTPHost != "mail.x" {
		t.Fatalf("persisted: %+v %v", set, err)
	}
	// Password with no secret key: refused naming the hint.
	cfNP := &cfFake{zones: zonesOK}
	sNP := cfgServer(t, cfNP, config.Config{AdminPassword: "pw"})
	cNP := sessionCookie(t, login(sNP, "pw", nil, ""))
	w = sNP.do(req{method: "POST", path: "/settings/smtp",
		body: url.Values{"smtp_host": {"mail.x"}, "mail_from": {"a@x.io"}, "smtp_pass": {"hunter2"}}.Encode(),
		hdr:  htmxPost, cookies: []*http.Cookie{cNP}})
	if w.Code != 422 || !strings.Contains(w.Body.String(), "SECRET_KEY") {
		t.Fatalf("pass without key: %d %s", w.Code, w.Body.String())
	}
	// Password with the key: sealed, and the page says it is set.
	cfg := gateCfg() // has SecretKey
	c2 := sessionCookie(t, login(s, "pw", nil, ""))
	_ = cfg
	s.deps.Cfg = cfg
	w = s.do(req{method: "POST", path: "/settings/smtp",
		body: url.Values{"smtp_host": {"mail.x"}, "mail_from": {"a@x.io"}, "smtp_pass": {"hunter2"}}.Encode(),
		hdr:  htmxPost, cookies: []*http.Cookie{c2}})
	if w.Code != 204 {
		t.Fatalf("sealed save: %d %s", w.Code, w.Body.String())
	}
	set, _ = s.deps.Store.GetSettings(context.Background())
	if set.SMTPPass == nil || string(set.SMTPPass) == "hunter2" {
		t.Fatalf("pass not sealed: %v", set.SMTPPass)
	}
	w = s.do(req{path: "/settings", cookies: []*http.Cookie{c2}})
	if !strings.Contains(w.Body.String(), "leave blank to keep") {
		t.Fatal("PassSet state not shown")
	}

	// Clearing both host and from stops delivery and drops the password.
	w = s.do(req{method: "POST", path: "/settings/smtp", body: url.Values{}.Encode(), hdr: htmxPost, cookies: []*http.Cookie{c2}})
	if w.Code != 204 {
		t.Fatalf("clear: %d", w.Code)
	}
	if set, _ = s.deps.Store.GetSettings(context.Background()); set.MailReady() || set.SMTPPass != nil {
		t.Fatalf("not cleared: %+v", set)
	}
}
