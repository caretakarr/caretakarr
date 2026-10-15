package web

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/caretakarr/caretakarr/internal/store"
)

func TestApplyControlsAllowlist(t *testing.T) {
	tests := []struct {
		name, in, want, errHas string
	}{
		{"empty is open", "", "", ""},
		{"cidrs kept", "192.168.0.0/16\n10.0.0.0/8", "192.168.0.0/16\n10.0.0.0/8", ""},
		{"bare ip becomes host prefix", "192.168.1.5\nfd00::1", "192.168.1.5/32\nfd00::1/128", ""},
		{"blank lines and crlf dropped", "10.0.0.0/8\r\n\r\n172.16.0.0/12\r\n", "10.0.0.0/8\n172.16.0.0/12", ""},
		{"masked host bits normalised", "192.168.1.5/24", "192.168.1.0/24", ""},
		{"garbage rejected", "10.0.0.0/8\nnot-a-cidr", "", "not-a-cidr"},
		{"bad mask rejected", "10.0.0.0/33", "", "10.0.0.0/33"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r store.Route
			err := applyControls(&r, url.Values{"allowlist": {tt.in}})
			if tt.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errHas) {
					t.Fatalf("want error containing %q, got %v", tt.errHas, err)
				}
				return
			}
			if err != nil || r.Allowlist != tt.want {
				t.Fatalf("got %q, %v", r.Allowlist, err)
			}
		})
	}
}

func TestApplyControlsBasicAuth(t *testing.T) {
	var r store.Route
	if err := applyControls(&r, url.Values{"basic_user": {"alice"}, "basic_password": {"s3cret"}}); err != nil {
		t.Fatal(err)
	}
	if r.BasicUser != "alice" || bcrypt.CompareHashAndPassword([]byte(r.BasicHash), []byte("s3cret")) != nil {
		t.Fatalf("%+v", r)
	}
	hash := r.BasicHash

	// edit with an empty password keeps the existing hash
	if err := applyControls(&r, url.Values{"basic_user": {"alice"}}); err != nil || r.BasicHash != hash {
		t.Fatalf("hash not kept: %v %q", err, r.BasicHash)
	}
	// a new password replaces it
	if err := applyControls(&r, url.Values{"basic_user": {"alice"}, "basic_password": {"other"}}); err != nil || r.BasicHash == hash {
		t.Fatalf("hash not replaced: %v", err)
	}
	// an empty user clears both
	if err := applyControls(&r, url.Values{}); err != nil || r.BasicUser != "" || r.BasicHash != "" {
		t.Fatalf("not cleared: %+v %v", r, err)
	}
	for name, v := range map[string]url.Values{
		"user without password":  {"basic_user": {"bob"}},
		"colon in user":          {"basic_user": {"a:b"}, "basic_password": {"x"}},
		"password over 72 bytes": {"basic_user": {"bob"}, "basic_password": {strings.Repeat("x", 73)}},
	} {
		var fresh store.Route
		if err := applyControls(&fresh, v); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestControlsEndToEndInHandlers(t *testing.T) {
	s, _, d := newDNSServer(t)
	ctx := context.Background()
	r := addRoute(t, s, d, "allowlist", "192.168.0.0/16", "basic_user", "alice", "basic_password", "s3cret")
	got, _ := s.deps.Store.GetRoute(ctx, r.ID)
	if got.Allowlist != "192.168.0.0/16" || got.BasicUser != "alice" || got.BasicHash == "" || strings.Contains(got.BasicHash, "s3cret") {
		t.Fatalf("%+v", got)
	}

	for _, path := range []string{"/", "/routes/" + itoa(r.ID)} {
		body := s.do(req{path: path}).Body.String()
		if strings.Contains(body, got.BasicHash) || strings.Contains(body, "s3cret") {
			t.Errorf("%s leaks the hash or password", path)
		}
	}
	if body := s.do(req{path: "/routes/" + itoa(r.ID)}).Body.String(); !strings.Contains(body, "192.168.0.0/16") || !strings.Contains(body, "alice") {
		t.Errorf("edit page lost allowlist or user")
	}

	// editing other fields with a blank password field keeps auth
	e := form("domain_id", itoa(d.ID), "enabled", "on", "allowlist", "192.168.0.0/16", "basic_user", "alice", "upstream_port", "9999")
	if w := s.do(req{method: "PUT", path: "/routes/" + itoa(r.ID), body: e.Encode(), hdr: htmxPost}); w.Code != 200 {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	if after, _ := s.deps.Store.GetRoute(ctx, r.ID); after.BasicHash != got.BasicHash || after.UpstreamPort != 9999 {
		t.Errorf("%+v", after)
	}

	// invalid CIDR is a 422 and changes nothing
	bad := form("domain_id", itoa(d.ID), "enabled", "on", "allowlist", "nope")
	if w := s.do(req{method: "PUT", path: "/routes/" + itoa(r.ID), body: bad.Encode(), hdr: htmxPost}); w.Code != 422 {
		t.Errorf("invalid cidr: %d", w.Code)
	}
}

func TestToggleRoute(t *testing.T) {
	s, _, d := newDNSServer(t)
	r := addRoute(t, s, d)
	toggle := func() {
		if w := s.do(req{method: "POST", path: "/routes/" + itoa(r.ID) + "/toggle", hdr: htmxPost}); w.Code != 204 {
			t.Fatalf("%d %q", w.Code, w.Body.String())
		}
	}
	toggle()
	if got, _ := s.deps.Store.GetRoute(context.Background(), r.ID); got.Enabled {
		t.Error("still enabled")
	}
	if body := s.do(req{path: "/"}).Body.String(); !strings.Contains(body, `aria-label="Enable"`) {
		t.Errorf("enable button missing")
	}
	toggle()
	if got, _ := s.deps.Store.GetRoute(context.Background(), r.ID); !got.Enabled {
		t.Error("still disabled")
	}
}

func TestApplyControlsProxyTuning(t *testing.T) {
	var r store.Route
	if err := applyControls(&r, url.Values{"stream": {"on"}, "max_body": {" 2gb "}}); err != nil || !r.Stream || r.MaxBody != 2<<30 {
		t.Fatalf("%+v %v", r, err)
	}
	if err := applyControls(&r, url.Values{}); err != nil || r.Stream || r.MaxBody != 0 {
		t.Fatalf("not cleared: %+v %v", r, err)
	}
	for _, bad := range []string{"2XB", "0", "101GB", "-5MB"} {
		if err := applyControls(&store.Route{}, url.Values{"max_body": {bad}}); err == nil || !strings.Contains(err.Error(), "upload limit") {
			t.Errorf("max_body %q: %v", bad, err)
		}
	}
}

func TestProxyTuningInHandlersAndPages(t *testing.T) {
	s, _, d := newDNSServer(t)
	r := addRoute(t, s, d, "stream", "on", "max_body", "2GB")
	got, _ := s.deps.Store.GetRoute(context.Background(), r.ID)
	if !got.Stream || got.MaxBody != 2<<30 {
		t.Fatalf("%+v", got)
	}
	if body := s.do(req{path: "/routes/" + itoa(r.ID)}).Body.String(); !strings.Contains(body, `value="2GB"`) || !strings.Contains(body, "name=\"stream\" checked") {
		t.Errorf("edit page lost the options")
	}
	if body := s.do(req{path: "/"}).Body.String(); !strings.Contains(body, ">stream<") || !strings.Contains(body, "max 2GB") {
		t.Errorf("dashboard badges missing")
	}
	if body := s.do(req{path: "/sites/new"}).Body.String(); !strings.Contains(body, `name="max_body"`) || !strings.Contains(body, `name="stream"`) {
		t.Errorf("add site form lacks the options")
	}
}
