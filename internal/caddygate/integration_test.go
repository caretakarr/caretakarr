// In-process Caddy integration for the gate module: portal on the route's own host, magic link to
// cookie, revocation. Serial: same rules as every caddytest-based package test.
package caddygate

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/caretakarr/caretakarr/internal/apply"
	"github.com/caretakarr/caretakarr/internal/caddycfg"
	"github.com/caretakarr/caretakarr/internal/caddytest"
	"github.com/caretakarr/caretakarr/internal/store"
)

func TestGateBehindRealCaddy(t *testing.T) {
	storage := t.TempDir() // before Cleanup so it is removed after Caddy stops
	caddytest.Cleanup(t)
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	d, err := st.CreateDomain(ctx, store.Domain{Name: "example.test", DefaultIP: "192.168.1.1", CertMode: "per_host"})
	if err != nil {
		t.Fatal(err)
	}
	upstream := caddytest.Upstream(t, "SECRET PAGE")
	upHost, upPort, _ := net.SplitHostPort(upstream)
	upN, _ := strconv.Atoi(upPort)
	acl, err := st.CreateRoute(ctx, store.Route{DomainID: d.ID, Subdomain: "acl", Scheme: "http",
		UpstreamHost: upHost, UpstreamPort: upN, Enabled: true, GateMode: "acl"})
	if err != nil {
		t.Fatal(err)
	}
	m := newFakeMailer()
	httpsPort := caddytest.FreePort(t)
	Install(New(st, func() (Mailer, string, bool) { return m, "access@example.com", true },
		"gate-test-secret-0123456789abcdef-1", httpsPort))
	svc := apply.New(st, caddycfg.Options{UIHost: "caretakarr.example.test", UIUpstream: "127.0.0.1:1", Email: "me@example.test",
		Issuer: caddycfg.Internal, HTTPPort: caddytest.FreePort(t), HTTPSPort: httpsPort, StoragePath: storage})
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	caddytest.WaitCert(t, httpsPort, "acl.example.test")
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "acl.example.test"},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", httpsPort))
			},
		}}
	do := func(method, target, form string, cookie *http.Cookie) (*http.Response, string) {
		t.Helper()
		var body io.Reader
		if form != "" {
			body = strings.NewReader(form)
		}
		req, err := http.NewRequest(method, "https://acl.example.test"+target, body)
		if err != nil {
			t.Fatal(err)
		}
		if form != "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.Host = "acl.example.test"
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	// Unauthenticated: the portal page, never the upstream.
	if _, body := do("GET", "/", "", nil); !strings.Contains(body, "Send me a link") {
		t.Fatalf("portal: %s", body)
	}

	// Enumeration: authorized and unauthorized responses are byte-identical.
	_, bodyDeny := do("POST", "/_gate/request", "email=bob@example.com", nil)
	if !strings.Contains(bodyDeny, "Check your email") {
		t.Fatalf("deny page: %s", bodyDeny)
	}
	if err := st.ReplaceGrants(ctx, acl.ID, []store.Grant{{Mode: "email", Value: "alice@example.com"}}); err != nil {
		t.Fatal(err)
	}
	_, bodyAllow := do("POST", "/_gate/request", "email=alice@example.com", nil)
	if bodyAllow != bodyDeny {
		t.Fatalf("enumeration leak:\n- deny  : %q\n- allow: %q", bodyDeny, bodyAllow)
	}
	_, token := m.link(t)

	// Magic link verifies, sets the session cookie, redirects to the site root.
	resp, _ := do("GET", "/_gate/verify/"+token, "", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("verify redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly {
		t.Fatalf("cookie: %+v", cookie)
	}
	if e, ok, err := st.Session(ctx, sha256hex(cookie.Value), time.Now().UTC().Format(time.RFC3339)); err != nil || !ok || e != "alice@example.com" {
		t.Fatalf("direct session check: %q %v %v", e, ok, err)
	}

	// The same session reaches the upstream and the second request does too (sliding TTL touch).
	for range 2 {
		resp, body := do("GET", "/somepath?q=1", "", cookie)
		if resp.StatusCode != 200 || !strings.Contains(body, "SECRET PAGE") {
			t.Fatalf("upstream: %d %q", resp.StatusCode, body)
		}
	}

	// Replay of the verified link: expired page, no fresh cookie.
	resp2, bodyR := do("GET", "/_gate/verify/"+token, "", nil)
	if !strings.Contains(bodyR, "expired or was already used") || strings.Contains(resp2.Header.Get("Set-Cookie"), CookieName) {
		t.Fatalf("replay: %d %q", resp2.StatusCode, bodyR)
	}

	// Revoke: the identity row dies, the cookie stops working immediately.
	if err := st.DeleteIdentity(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	_, bodyV := do("GET", "/", "", cookie)
	if !strings.Contains(bodyV, "Send me a link") {
		t.Fatalf("after revoke: %q", bodyV)
	}
}
