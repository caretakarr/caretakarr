package caddygate

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/caretakarr/caretakarr/internal/store"
)

var nowStr = "2026-10-15T10:00:00Z"
var nextBody = "UPSTREAM-CONTENT"

func nowUTC() time.Time { return time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC) }

type sent struct{ To, Body string }

// fakeMailer is the Mailer the flow tests use. Send records and wakes the channel non-blockingly,
// so a slower goroutine (deliver is detached) still signals deterministically.
type fakeMailer struct {
	mu  sync.Mutex
	ch  chan struct{}
	all []sent
}

func newFakeMailer() *fakeMailer { return &fakeMailer{ch: make(chan struct{}, 8)} }

func (m *fakeMailer) Send(from, to, subject, body string) error {
	m.mu.Lock()
	m.all = append(m.all, sent{To: to, Body: body})
	m.mu.Unlock()
	select {
	case m.ch <- struct{}{}:
	default:
	}
	return nil
}

// link waits for exactly one mail and returns its recipient and magic token without sleeping on
// a fixed delay: the channel is fed by deliver itself.
func (m *fakeMailer) link(t *testing.T) (to, token string) {
	t.Helper()
	select {
	case <-m.ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no mail arrived")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.all[len(m.all)-1]
	match := regexp.MustCompile(`https://\S+` + regexp.QuoteMeta(Prefix) + `/verify/(\S+)`).FindStringSubmatch(s.Body)
	if len(match) < 2 {
		t.Fatalf("no link in mail: %s", s.Body)
	}
	return s.To, match[1]
}

// gateStore opens a store with one ACL route and one open route on distinct hosts.
func gateStore(t *testing.T) (*store.Store, int64, int64) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	d, err := st.CreateDomain(ctx, store.Domain{Name: "example.com", DefaultIP: "192.168.1.1", CertMode: "wildcard"})
	if err != nil {
		t.Fatal(err)
	}
	acl, err := st.CreateRoute(ctx, store.Route{DomainID: d.ID, Subdomain: "acl", Scheme: "https",
		UpstreamHost: "192.168.1.20", UpstreamPort: 8989, Enabled: true, GateMode: "acl"})
	if err != nil {
		t.Fatal(err)
	}
	open, err := st.CreateRoute(ctx, store.Route{DomainID: d.ID, Subdomain: "open", Scheme: "https",
		UpstreamHost: "192.168.1.21", UpstreamPort: 9000, Enabled: true, GateMode: "open"})
	if err != nil {
		t.Fatal(err)
	}
	return st, acl.ID, open.ID
}

// gateService wires a deterministic Service with a fake mailer behind the settings source.
func gateService(t *testing.T, st *store.Store) (*Service, *fakeMailer) {
	t.Helper()
	m := newFakeMailer()
	s := New(st, func() (Mailer, string, bool) { return m, "access@example.com", true },
		"e2e-secret-key-0123456789abcdef-e2e", 443)
	s.now = nowUTC
	return s, m
}

// call drives one request at the Service (module-equivalent), returning the response and whether
// the upstream handler ran.
func (s *Service) call(t *testing.T, h Handler, method, target, form string, cookie *http.Cookie, secure bool, upstream string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	r, err := http.NewRequest(method, "https://st.example.com"+target, strings.NewReader(form))
	if err != nil {
		t.Fatal(err)
	}
	r.RequestURI = target
	if form != "" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Host = h.Host
	r.RemoteAddr = "203.0.113.9:34567"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if secure {
		r.TLS = new(tls.ConnectionState)
	}
	reached := false
	w := httptest.NewRecorder()
	s.Wrap(h, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		fmt.Fprint(w, upstream)
	})).ServeHTTP(w, r)
	return w, reached
}

const aclEmail = "alice@example.com"

func TestGuardBlocksUpstreamWithoutSession(t *testing.T) {
	st, acl, _ := gateStore(t)
	s, _ := gateService(t, st)
	h := Handler{RouteID: acl, Host: "acl.example.com", Mode: "acl"}
	w, reached := s.call(t, h, "GET", "/", "", nil, false, nextBody)
	if reached {
		t.Fatal("upstream served without a session")
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Send me a link") {
		t.Fatalf("portal: %d %s", w.Code, w.Body.String())
	}
}

func TestFullMagicLinkFlowACL(t *testing.T) {
	st, acl, _ := gateStore(t)
	s, m := gateService(t, st)
	ctx := context.Background()
	if err := st.ReplaceGrants(ctx, acl, []store.Grant{{Mode: "email", Value: aclEmail}}); err != nil {
		t.Fatal(err)
	}
	h := Handler{RouteID: acl, Host: "acl.example.com", Mode: "acl"}

	// Unknown email: identical page, no mail, no token.
	w, reached := s.call(t, h, "POST", Prefix+"/request", "email=bob@example.com", nil, false, nextBody)
	if reached {
		t.Fatal("upstream reached from the request flow")
	}
	if !strings.Contains(w.Body.String(), "Check your email") {
		t.Fatalf("unauthorized: %s", w.Body.String())
	}
	m.mu.Lock()
	n := len(m.all)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("mail sent for an unqualified identity: %d", n)
	}

	// Authorized email: same generic page; token issued asynchronously.
	w, _ = s.call(t, h, "POST", Prefix+"/request", "email="+aclEmail, nil, false, nextBody)
	if !strings.Contains(w.Body.String(), "Check your email") {
		t.Fatalf("authorized: %s", w.Body.String())
	}
	to, token := m.link(t)
	if to != aclEmail {
		t.Fatalf("mail went to %s", to)
	}

	// The link verifies on its own host, sets the session cookie, redirects.
	w, _ = s.call(t, h, "GET", Prefix+"/verify/"+token, "", nil, false, nextBody)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("verify: %d %q", w.Code, w.Header().Get("Location"))
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Name != CookieName || !cookie.HttpOnly || cookie.MaxAge != int(30*24*time.Hour/time.Second) {
		t.Fatalf("cookie: %+v", cookie)
	}
	w, reached = s.call(t, h, "GET", "/", "", cookie, false, nextBody)
	if !reached {
		t.Fatalf("session did not pass the gate: %d %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != nextBody {
		t.Fatalf("wrong body: %q", got)
	}

	// Replay: the token is exactly once and never sets another cookie.
	w, _ = s.call(t, h, "GET", Prefix+"/verify/"+token, "", nil, false, nextBody)
	if strings.Contains(w.Header().Get("Set-Cookie"), CookieName) {
		t.Fatal("replayed link set a cookie")
	}
}

func TestTokenIsHostPinned(t *testing.T) {
	st, acl, open := gateStore(t)
	s, m := gateService(t, st)
	h := Handler{RouteID: open, Host: "open.example.com", Mode: "open"}
	if _, reached := s.call(t, h, "POST", Prefix+"/request", "email=anyone@x.io", nil, false, nextBody); reached {
		t.Fatal("upstream reached")
	}
	_, token := m.link(t)
	// The token was pinned to open.example.com; a verify at the ACL host must not mint.
	aclH := Handler{RouteID: acl, Host: "acl.example.com", Mode: "acl"}
	w, reached := s.call(t, aclH, "GET", Prefix+"/verify/"+token, "", nil, false, nextBody)
	if reached {
		t.Fatal("upstream reached")
	}
	if strings.Contains(w.Header().Get("Set-Cookie"), CookieName) {
		t.Fatal("cookie set from a foreign-host token")
	}
}

func TestOpenRouteAnyEmail(t *testing.T) {
	st, _, open := gateStore(t)
	s, m := gateService(t, st)
	h := Handler{RouteID: open, Host: "open.example.com", Mode: "open"}
	if _, reached := s.call(t, h, "POST", Prefix+"/request", "email=passerby@nowhere.org", nil, false, nextBody); reached {
		t.Fatal("upstream reached")
	}
	_, token := m.link(t)
	w, _ := s.call(t, h, "GET", Prefix+"/verify/"+token, "", nil, false, nextBody)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("verify: %d", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if cookie.MaxAge != int(7*24*time.Hour/time.Second) {
		t.Fatalf("open window: %d", cookie.MaxAge)
	}
	if _, reached := s.call(t, h, "GET", "/", "", cookie, false, nextBody); !reached {
		t.Fatal("open-route session rejected")
	}
}

func TestCrossRouteIsolationAndSharing(t *testing.T) {
	st, acl, open := gateStore(t)
	s, m := gateService(t, st)
	ctx := context.Background()
	// alice holds a grant on the ACL route but not on the open one; open routes need none.
	if err := st.ReplaceGrants(ctx, acl, []store.Grant{{Mode: "email", Value: aclEmail}}); err != nil {
		t.Fatal(err)
	}
	openH := Handler{RouteID: open, Host: "open.example.com", Mode: "open"}
	aclH := Handler{RouteID: acl, Host: "acl.example.com", Mode: "acl"}

	// Session minted at the open route…
	if _, reached := s.call(t, openH, "POST", Prefix+"/request", "email=other@x.io", nil, false, nextBody); reached {
		t.Fatal("upstream reached")
	}
	_, token := m.link(t)
	w, _ := s.call(t, openH, "GET", Prefix+"/verify/"+token, "", nil, false, nextBody)
	cookie := w.Result().Cookies()[0]

	// …is refused on the ACL route (no grant for other@x.io).
	if _, reached := s.call(t, aclH, "GET", "/", "", cookie, false, nextBody); reached {
		t.Fatal("a session without a grant crossed onto the ACL route")
	}

	// A session minted for alice is shared: the same cookie value works on both routes.
	if _, reached := s.call(t, openH, "POST", Prefix+"/request", "email="+aclEmail, nil, false, nextBody); reached {
		t.Fatal("upstream reached")
	}
	_, token = m.link(t)
	w, _ = s.call(t, openH, "GET", Prefix+"/verify/"+token, "", nil, false, nextBody)
	al := w.Result().Cookies()[0]
	if _, reached := s.call(t, aclH, "GET", "/", "", al, false, nextBody); !reached {
		t.Fatal("shared session rejected on the granted route")
	}
}

func TestSecureCookieOverTLS(t *testing.T) {
	st, _, open := gateStore(t)
	s, m := gateService(t, st)
	h := Handler{RouteID: open, Host: "open.example.com", Mode: "open"}
	if _, reached := s.call(t, h, "POST", Prefix+"/request", "email=a@b.c", nil, false, nextBody); reached {
		t.Fatal("upstream reached")
	}
	_, token := m.link(t)
	w, _ := s.call(t, h, "GET", Prefix+"/verify/"+token, "", nil, true, nextBody) // TLS: Secure must be set
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure {
		t.Fatalf("cookie not Secure over TLS: %+v", cookie)
	}
}

func TestFailClosedOnStoreErrors(t *testing.T) {
	st, acl, _ := gateStore(t)
	s, _ := gateService(t, st)
	h := Handler{RouteID: acl, Host: "acl.example.com", Mode: "acl"}
	_ = st.Close()
	w, reached := s.call(t, h, "GET", "/", "", &http.Cookie{Name: CookieName, Value: "junk"}, false, nextBody)
	if reached {
		t.Fatal("upstream served with a broken store")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("fail-closed status: %d", w.Code)
	}
}

func TestRateLimits(t *testing.T) {
	// The email bucket: at 5/h a sixth request for the same address is refused.
	s := &Service{lim: limiter{}, now: nowUTC}
	for range 5 {
		if !s.lim.allow("email:x", 5, time.Hour, s.now()) {
			t.Fatal("5th allowed request denied")
		}
	}
	if s.lim.allow("email:x", 5, time.Hour, s.now()) {
		t.Fatal("6th request allowed")
	}
	// Other keys are independent.
	if !s.lim.allow("email:y", 5, time.Hour, s.now()) {
		t.Fatal("independent keys share buckets")
	}
	if !s.lim.allow("email:x", 5, time.Hour, nowUTC().Add(2*time.Hour)) {
		t.Fatal("window did not open after expiry")
	}
}

func TestSessionSecretDerivationStableAndRotating(t *testing.T) {
	st, _, _ := gateStore(t)
	ctx := context.Background()
	s := New(st, func() (Mailer, string, bool) { return nil, "", false }, "e2e-secret-key-0123456789abcdef-e2e", 443)
	if err := st.UpsertIdentity(ctx, "a@b.c", "n1", sha256hex(s.sessionSecret("a@b.c", "n1")), nowStr, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	// Same email, same nonce: identical secret (shared session across hosts).
	if got := s.sessionSecret("a@b.c", "n1"); got != s.sessionSecret("a@b.c", "n1") || got == "" {
		t.Fatal("derivation not stable")
	}
	// New nonce (a fresh identity row): a different secret, so revival after revoke is safe.
	if s.sessionSecret("a@b.c", "n2") == s.sessionSecret("a@b.c", "n1") {
		t.Fatal("rotation did not produce a new secret")
	}
	if b := s.sessionSecret("a@b.c", "n1"); len(b) != 32 {
		t.Fatal("derived secret is not 32 bytes")
	}
}

func TestIssuanceRefusedWithoutConfig(t *testing.T) {
	st, acl, _ := gateStore(t)
	ctx := context.Background()
	if err := st.ReplaceGrants(ctx, acl, []store.Grant{{Mode: "email", Value: aclEmail}}); err != nil {
		t.Fatal(err)
	}
	h := Handler{RouteID: acl, Host: "acl.example.com", Mode: "acl"}
	s := New(st, func() (Mailer, string, bool) { return nil, "", false }, "", 443) // nothing configured
	s.now = nowUTC
	w, reached := s.call(t, h, "POST", Prefix+"/request", "email="+aclEmail, nil, false, nextBody)
	if reached {
		t.Fatal("upstream reached during a failed issuance")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("issuance with no config: %d", w.Code)
	}
}
