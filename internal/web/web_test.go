package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/caretakarr/caretakarr/internal/config"
	"github.com/caretakarr/caretakarr/internal/store"
)

func newServer(t *testing.T, password string) *Server {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(Deps{Cfg: config.Config{AdminPassword: password}, Store: st})
}

// The login page shows no app navigation: the visitor is signed out.
func TestLoginPageIsBare(t *testing.T) {
	s := newServer(t, "secret")
	body := s.do(req{path: "/login"}).Body.String()
	if !strings.Contains(body, `name="password"`) {
		t.Fatalf("not the login form: %s", body)
	}
	for _, leak := range []string{"Sign out", `href="/domains"`, `href="/logs"`, `href="/security"`, `href="/sites/new"`} {
		if strings.Contains(body, leak) {
			t.Errorf("login page shows signed-out visitor %q", leak)
		}
	}
}

type req struct {
	method, path, body string
	peer               string
	host               string // default caretakarr.example.com
	hdr                map[string]string
	cookies            []*http.Cookie
}

func (s *Server) do(r req) *httptest.ResponseRecorder {
	if r.method == "" {
		r.method = "GET"
	}
	hr := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
	hr.Host = "caretakarr.example.com"
	if r.host != "" {
		hr.Host = r.host
	}
	if r.peer != "" {
		hr.RemoteAddr = r.peer
	} else {
		hr.RemoteAddr = "127.0.0.1:5555"
	}
	if r.body != "" {
		hr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range r.hdr {
		hr.Header.Set(k, v)
	}
	for _, c := range r.cookies {
		hr.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, hr)
	return w
}

var htmxPost = map[string]string{"HX-Request": "true", "Origin": "https://caretakarr.example.com"}

func login(s *Server, password string, extra map[string]string, peer string) *httptest.ResponseRecorder {
	h := map[string]string{}
	for k, v := range htmxPost {
		h[k] = v
	}
	for k, v := range extra {
		h[k] = v
	}
	return s.do(req{method: "POST", path: "/login", body: url.Values{"password": {password}}.Encode(), hdr: h, peer: peer})
}

func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func TestLoginSuccessSetsSecureCookie(t *testing.T) {
	s := newServer(t, "pw")
	w := login(s, "pw", map[string]string{"X-Forwarded-Proto": "https"}, "")
	if w.Code != 204 || w.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	c := sessionCookie(t, w)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.MaxAge != 12*3600 {
		t.Errorf("cookie flags: %+v", c)
	}
}

func TestLoginFailure(t *testing.T) {
	s := newServer(t, "pw")
	if w := login(s, "wrong", nil, ""); w.Code != 401 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestLoginThrottle(t *testing.T) {
	s := newServer(t, "pw")
	now := time.Unix(1_000_000, 0)
	s.auth.now = func() time.Time { return now }
	for range maxFailures {
		login(s, "bad", nil, "")
	}
	if w := login(s, "pw", nil, ""); w.Code != 429 {
		t.Fatalf("throttled IP got %d", w.Code)
	}
	if w := login(s, "pw", map[string]string{"X-Forwarded-For": "9.9.9.9"}, ""); w.Code != 204 {
		t.Fatalf("other client IP (XFF via loopback) got %d", w.Code)
	}
	now = now.Add(throttleWindow + time.Second)
	if w := login(s, "pw", nil, ""); w.Code != 204 {
		t.Fatalf("after window got %d", w.Code)
	}
}

func TestXFFIgnoredFromNonLoopbackPeer(t *testing.T) {
	s := newServer(t, "pw")
	for i := range maxFailures { // forged XFF must not dodge the throttle
		login(s, "bad", map[string]string{"X-Forwarded-For": "1.2.3." + string(rune('0'+i))}, "10.0.0.5:4444")
	}
	if w := login(s, "pw", map[string]string{"X-Forwarded-For": "7.7.7.7"}, "10.0.0.5:4444"); w.Code != 429 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestProtectedRoutes(t *testing.T) {
	s := newServer(t, "pw")
	w := s.do(req{path: "/"})
	if w.Code != 303 || w.Header().Get("Location") != "/login" {
		t.Fatalf("unauthenticated: %d %v", w.Code, w.Header())
	}
	w = s.do(req{path: "/", hdr: map[string]string{"HX-Request": "true"}})
	if w.Code != 401 || w.Header().Get("HX-Redirect") != "/login" {
		t.Fatalf("htmx unauthenticated: %d %v", w.Code, w.Header())
	}
	c := sessionCookie(t, login(s, "pw", nil, ""))
	w = s.do(req{path: "/", cookies: []*http.Cookie{c}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Dashboard") {
		t.Fatalf("authenticated: %d", w.Code)
	}
	forged := &http.Cookie{Name: sessionCookieName, Value: "9999999999.deadbeef"}
	if w := s.do(req{path: "/", cookies: []*http.Cookie{forged}}); w.Code != 303 {
		t.Fatalf("forged cookie accepted: %d", w.Code)
	}
}

func TestLoginPageAndStatic(t *testing.T) {
	s := newServer(t, "pw")
	if w := s.do(req{path: "/login"}); w.Code != 200 || !strings.Contains(w.Body.String(), "password") {
		t.Fatalf("login page: %d", w.Code)
	}
	for _, p := range []string{"/static/htmx.min.js", "/static/pico.min.css"} {
		if w := s.do(req{path: p}); w.Code != 200 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestCSRF(t *testing.T) {
	s := newServer(t, "pw")
	body := url.Values{"password": {"pw"}}.Encode()
	cases := map[string]map[string]string{
		"no HX-Request":    {"Origin": "https://caretakarr.example.com"},
		"wrong origin":     {"HX-Request": "true", "Origin": "https://evil.example.org"},
		"malformed origin": {"HX-Request": "true", "Origin": "::::"},
	}
	for name, h := range cases {
		if w := s.do(req{method: "POST", path: "/login", body: body, hdr: h}); w.Code != 403 {
			t.Errorf("%s: got %d", name, w.Code)
		}
	}
}

func TestOpenModeBanner(t *testing.T) {
	s := newServer(t, "")
	w := s.do(req{path: "/"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "ADMIN_PASSWORD") {
		t.Fatalf("open mode: %d %s", w.Code, w.Body.String())
	}
	s = newServer(t, "pw")
	c := sessionCookie(t, login(s, "pw", nil, ""))
	if strings.Contains(s.do(req{path: "/", cookies: []*http.Cookie{c}}).Body.String(), "ADMIN_PASSWORD") {
		t.Error("open-mode banner shown with password set")
	}
}

func TestStartupErrorBanner(t *testing.T) {
	s := newServer(t, "")
	s.startupErr = func() error { return errors.New("caddy said no: bad cidr") }
	if body := s.do(req{path: "/"}).Body.String(); !strings.Contains(body, "caddy said no: bad cidr") {
		t.Fatalf("banner missing: %s", body)
	}
}
