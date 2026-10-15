package store

import (
	"context"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustDomain(t *testing.T, s *Store, name string) Domain {
	t.Helper()
	d, err := s.CreateDomain(context.Background(), Domain{Name: name, ZoneID: "z", DefaultIP: "10.0.0.1", CertMode: "wildcard"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func route(domainID int64, sub string) Route {
	return Route{DomainID: domainID, Subdomain: sub, Scheme: "http", UpstreamHost: "192.168.1.20", UpstreamPort: 8989, Enabled: true}
}

func TestMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustDomain(t, s, "example.com")
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 6 {
		t.Fatalf("user_version %d %v", v, err)
	}
	s.Close()
	s, err = Open(path) // reopen: migrations must not re-run (would fail on CREATE TABLE) or lose data
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ds, err := s.ListDomains(context.Background())
	if err != nil || len(ds) != 1 {
		t.Fatalf("domains %v %v", ds, err)
	}
}

func TestDomainCRUD(t *testing.T) {
	s, ctx := open(t), context.Background()
	d := mustDomain(t, s, "example.com")
	got, err := s.GetDomain(ctx, d.ID)
	if err != nil || got.Name != "example.com" || got.CertMode != "wildcard" || got.CreatedAt == "" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.CreateDomain(ctx, Domain{Name: "example.com", ZoneID: "z", DefaultIP: "1.1.1.1", CertMode: "wildcard"}); err == nil {
		t.Error("duplicate name accepted")
	}
	if _, err := s.CreateDomain(ctx, Domain{Name: "b.com", ZoneID: "z", DefaultIP: "1.1.1.1", CertMode: "bogus"}); err == nil {
		t.Error("bad cert_mode accepted")
	}
	d.CertMode = "per_host"
	if err := s.UpdateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetDomain(ctx, d.ID); got.CertMode != "per_host" {
		t.Errorf("update lost: %+v", got)
	}
	if err := s.DeleteDomain(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDomain(ctx, d.ID); err == nil {
		t.Error("deleted domain still found")
	}
}

func TestRouteCRUDAndConstraints(t *testing.T) {
	s, ctx := open(t), context.Background()
	d := mustDomain(t, s, "example.com")

	r := route(d.ID, "sonarr")
	r.RecordIP, r.Allowlist, r.BasicUser, r.BasicHash, r.Preset = "10.9.9.9", "192.168.0.0/16", "u", "h", "Sonarr"
	r.Stream, r.MaxBody = true, 2<<30
	r, err := s.CreateRoute(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRoute(ctx, r.ID)
	if err != nil || got != r {
		t.Fatalf("round trip: %+v vs %+v (%v)", got, r, err)
	}

	if _, err := s.CreateRoute(ctx, route(d.ID, "sonarr")); err == nil {
		t.Error("duplicate (domain, subdomain) accepted")
	}
	for _, p := range []int{0, 65536} {
		bad := route(d.ID, "p")
		bad.UpstreamPort = p
		if _, err := s.CreateRoute(ctx, bad); err == nil {
			t.Errorf("port %d accepted", p)
		}
	}
	bad := route(d.ID, "s")
	bad.Scheme = "ftp"
	if _, err := s.CreateRoute(ctx, bad); err == nil {
		t.Error("bad scheme accepted")
	}

	got.Enabled, got.RecordIP, got.BasicUser, got.BasicHash = false, "", "", ""
	if err := s.UpdateRoute(ctx, got); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetRoute(ctx, r.ID)
	if after.Enabled || after.RecordIP != "" || after.BasicUser != "" {
		t.Errorf("update/NULL round trip: %+v", after)
	}

	if _, err := s.CreateRoute(ctx, route(d.ID, "radarr")); err != nil {
		t.Fatal(err)
	}
	rs, err := s.ListRoutes(ctx)
	if err != nil || len(rs) != 2 || rs[0].Subdomain != "radarr" {
		t.Fatalf("list (ordered by domain, subdomain): %+v %v", rs, err)
	}

	if err := s.DeleteDomain(ctx, d.ID); err == nil {
		t.Error("domain with routes deleted (FK restrict)")
	}
	if err := s.DeleteRoute(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDNSRecordCascade(t *testing.T) {
	s, ctx := open(t), context.Background()
	d := mustDomain(t, s, "example.com")
	r, _ := s.CreateRoute(ctx, route(d.ID, "sonarr"))
	if _, err := s.GetDNS(ctx, r.ID); err == nil {
		t.Error("want not found before upsert")
	}
	rec := DNSRecord{RouteID: r.ID, CFRecordID: "cf1", Content: "10.0.0.1", Owned: true, State: "synced"}
	if err := s.UpsertDNS(ctx, rec); err != nil {
		t.Fatal(err)
	}
	rec.State, rec.Error = "error", "boom"
	if err := s.UpsertDNS(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDNS(ctx, r.ID)
	if err != nil || got.State != "error" || got.Error != "boom" || !got.Owned {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.UpsertDNS(ctx, DNSRecord{RouteID: r.ID, State: "weird"}); err == nil {
		t.Error("bad state accepted")
	}
	if err := s.DeleteRoute(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDNS(ctx, r.ID); err == nil {
		t.Error("dns record survived route delete")
	}
}

func TestTxRollback(t *testing.T) {
	s, ctx := open(t), context.Background()
	err := s.Tx(ctx, func(tx *Store) error {
		if _, err := tx.CreateDomain(ctx, Domain{Name: "a.com", ZoneID: "z", DefaultIP: "1.1.1.1", CertMode: "wildcard"}); err != nil {
			return err
		}
		return context.Canceled
	})
	if err == nil {
		t.Fatal("want error")
	}
	if ds, _ := s.ListDomains(ctx); len(ds) != 0 {
		t.Errorf("rollback failed: %+v", ds)
	}
}

func TestListAndDeleteDNS(t *testing.T) {
	s, ctx := open(t), context.Background()
	d := mustDomain(t, s, "example.com")
	a, _ := s.CreateRoute(ctx, route(d.ID, "a"))
	b, _ := s.CreateRoute(ctx, route(d.ID, "b"))
	for _, r := range []Route{a, b} {
		if err := s.UpsertDNS(ctx, DNSRecord{RouteID: r.ID, State: "synced", Owned: true}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListDNS(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("%v %v", all, err)
	}
	if err := s.DeleteDNS(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.ListDNS(ctx); len(all) != 1 || all[0].RouteID != b.ID {
		t.Fatalf("%v", all)
	}
}

func TestUIDNSRecord(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, err := s.GetUIDNS(ctx); err == nil {
		t.Fatal("want not found before upsert")
	}
	rec := DNSRecord{CFRecordID: "cf1", Content: "192.168.1.5", Owned: true, State: "synced", SyncedAt: "now"}
	if err := s.UpsertUIDNS(ctx, rec); err != nil {
		t.Fatal(err)
	}
	rec.State, rec.Error, rec.Owned = "conflict", "points elsewhere", false
	if err := s.UpsertUIDNS(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUIDNS(ctx)
	if err != nil || got.State != "conflict" || got.Error != "points elsewhere" || got.Owned || got.CFRecordID != "cf1" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.UpsertUIDNS(ctx, DNSRecord{State: "weird"}); err == nil {
		t.Error("bad state accepted")
	}
	if _, err := s.db.Exec("INSERT INTO ui_dns_record (id, state) VALUES (2, 'pending')"); err == nil {
		t.Error("second row accepted")
	}
}

func TestTOTPLifecycle(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, ok, err := s.GetTOTP(ctx); ok || err != nil {
		t.Fatalf("empty: %v %v", ok, err)
	}
	if err := s.SaveTOTP(ctx, []byte("sealed1")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTOTP(ctx, []byte("sealed2")); err != nil { // unconfirmed seed is replaceable
		t.Fatal(err)
	}
	if ok, _ := s.AdvanceTOTPStep(ctx, 5); ok {
		t.Fatal("step accepted before enable")
	}
	if err := s.EnableTOTP(ctx, 10, []string{"h1", "h2"}); err != nil {
		t.Fatal(err)
	}
	got, ok, _ := s.GetTOTP(ctx)
	if !ok || !got.Enabled || string(got.Secret) != "sealed2" || got.LastStep != 10 {
		t.Fatalf("%+v", got)
	}
	if err := s.SaveTOTP(ctx, []byte("x")); err == nil {
		t.Error("enabled seed overwritten")
	}
	for _, c := range []struct {
		step int64
		want bool
	}{{10, false}, {9, false}, {11, true}, {11, false}} {
		if ok, _ := s.AdvanceTOTPStep(ctx, c.step); ok != c.want {
			t.Errorf("step %d: %v want %v", c.step, ok, c.want)
		}
	}
	if ok, _ := s.UseBackupCode(ctx, "h1"); !ok {
		t.Error("first use")
	}
	if ok, _ := s.UseBackupCode(ctx, "h1"); ok {
		t.Error("second use")
	}
	if ok, _ := s.UseBackupCode(ctx, "nope"); ok {
		t.Error("unknown code")
	}
	if n, _ := s.CountBackupCodes(ctx); n != 1 {
		t.Errorf("unused %d", n)
	}
	if err := s.EnableTOTP(ctx, 1, nil); err != nil { // idempotent update path
		t.Fatal(err)
	}
}

func TestPasskeysAndResetAuth(t *testing.T) {
	s, ctx := open(t), context.Background()
	p := Passkey{ID: "a", RPID: "h.example.com", CredentialID: []byte{1}, Credential: "{}", Name: "laptop"}
	if err := s.AddPasskey(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.ID = "b"
	if err := s.AddPasskey(ctx, p); err == nil {
		t.Error("duplicate credential id accepted")
	}
	if err := s.TouchPasskey(ctx, []byte{1}, `{"n":2}`); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.ListPasskeys(ctx)
	if len(ps) != 1 || ps[0].Credential != `{"n":2}` || ps[0].LastUsedAt == "" {
		t.Fatalf("%+v", ps)
	}
	if err := s.SaveTOTP(ctx, []byte("s")); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableTOTP(ctx, 1, []string{"x", "y"}); err != nil {
		t.Fatal(err)
	}
	totp, codes, keys, err := s.ResetAuth(ctx)
	if err != nil || !totp || codes != 2 || keys != 1 {
		t.Fatalf("%v %d %d %v", totp, codes, keys, err)
	}
	if ps, _ := s.ListPasskeys(ctx); len(ps) != 0 {
		t.Error("passkeys left")
	}
	if _, ok, _ := s.GetTOTP(ctx); ok {
		t.Error("totp left")
	}
	if ok, _ := s.DeletePasskey(ctx, "a"); ok {
		t.Error("deleted a ghost")
	}
}
