package web

import (
	"context"
	"strings"
	"testing"

	"github.com/caretakarr/caretakarr/internal/store"
)

func addRoute(t *testing.T, s *Server, d store.Domain, extra ...string) store.Route {
	t.Helper()
	kv := append([]string{"domain_id", itoa(d.ID), "enabled", "on"}, extra...)
	w := s.do(req{method: "POST", path: "/routes", body: form(kv...).Encode(), hdr: htmxPost})
	if w.Code != 204 {
		t.Fatalf("create route: %d %q", w.Code, w.Body.String())
	}
	rs, _ := s.deps.Store.ListRoutes(context.Background())
	return rs[len(rs)-1]
}

func dnsRow(t *testing.T, s *Server, routeID int64) store.DNSRecord {
	t.Helper()
	d, err := s.deps.Store.GetDNS(context.Background(), routeID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func newDNSServer(t *testing.T) (*Server, *cfFake, store.Domain) {
	t.Helper()
	cf := &cfFake{zones: zonesOK}
	s := domainServer(t, cf)
	d, err := s.deps.Store.CreateDomain(context.Background(), store.Domain{Name: "example.com", ZoneID: "zone123", DefaultIP: "10.0.0.1", CertMode: "wildcard"})
	if err != nil {
		t.Fatal(err)
	}
	return s, cf, d
}

func TestRouteCreateSyncsDNS(t *testing.T) {
	s, cf, d := newDNSServer(t)
	r := addRoute(t, s, d)
	recs := cf.dns.Records()
	if len(recs) != 1 || recs[0].Name != "sonarr.example.com" || recs[0].Content != "10.0.0.1" {
		t.Fatalf("%+v", recs)
	}
	if row := dnsRow(t, s, r.ID); row.State != "synced" || !row.Owned {
		t.Fatalf("%+v", row)
	}
	if body := s.do(req{path: "/"}).Body.String(); !strings.Contains(body, "synced") {
		t.Errorf("dashboard lacks DNS state: %s", body)
	}
}

func TestDNSFailureKeepsRouteAndRetryWorks(t *testing.T) {
	s, cf, d := newDNSServer(t)
	cf.dns.FailWith("cloudflare is down")
	r := addRoute(t, s, d)
	if rs, _ := s.deps.Store.ListRoutes(context.Background()); len(rs) != 1 {
		t.Fatal("route reverted by DNS failure")
	}
	if row := dnsRow(t, s, r.ID); row.State != "error" || !strings.Contains(row.Error, "cloudflare is down") {
		t.Fatalf("%+v", row)
	}
	if body := s.do(req{path: "/"}).Body.String(); !strings.Contains(body, "cloudflare is down") || !strings.Contains(body, "Retry") {
		t.Errorf("error/retry not shown: %s", body)
	}
	cf.dns.FailWith("")
	if w := s.do(req{method: "POST", path: "/routes/" + itoa(r.ID) + "/dns/resync", hdr: htmxPost}); w.Code != 204 {
		t.Fatalf("resync: %d", w.Code)
	}
	if row := dnsRow(t, s, r.ID); row.State != "synced" {
		t.Fatalf("%+v", row)
	}
}

func TestConflictAndTakeOver(t *testing.T) {
	s, cf, d := newDNSServer(t)
	cf.dns.Seed("sonarr.example.com", "9.9.9.9")
	r := addRoute(t, s, d)
	if row := dnsRow(t, s, r.ID); row.State != "conflict" {
		t.Fatalf("%+v", row)
	}
	if body := s.do(req{path: "/"}).Body.String(); !strings.Contains(body, "Take over") {
		t.Errorf("take over action missing")
	}
	if w := s.do(req{method: "POST", path: "/routes/" + itoa(r.ID) + "/dns/takeover", hdr: htmxPost}); w.Code != 204 {
		t.Fatalf("takeover: %d", w.Code)
	}
	if row := dnsRow(t, s, r.ID); row.State != "synced" || !row.Owned || cf.dns.Records()[0].Content != "10.0.0.1" {
		t.Fatalf("%+v %+v", row, cf.dns.Records())
	}
}

func TestDeleteConfirmationAndRecordHandling(t *testing.T) {
	// owned: the dialog button knows the record and offers to delete it, and it is
	s, cf, d := newDNSServer(t)
	r := addRoute(t, s, d)
	if body := s.do(req{path: "/routes/" + itoa(r.ID)}).Body.String(); !strings.Contains(body, `data-dns-ip="10.0.0.1"`) {
		t.Errorf("owned record: dialog data missing: %s", body)
	}
	if w := s.do(req{method: "DELETE", path: "/routes/" + itoa(r.ID) + "?delete_dns=on", hdr: htmxPost}); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if len(cf.dns.Records()) != 0 {
		t.Error("owned record not deleted")
	}

	// adopted: no dns data on the button; the dialog explains the record is left in place
	s, cf, d = newDNSServer(t)
	cf.dns.Seed("sonarr.example.com", "10.0.0.1")
	r = addRoute(t, s, d)
	if body := s.do(req{path: "/routes/" + itoa(r.ID)}).Body.String(); strings.Contains(body, `data-dns-ip=`) {
		t.Errorf("adopted record offered for deletion: %s", body)
	}
	s.do(req{method: "DELETE", path: "/routes/" + itoa(r.ID), hdr: htmxPost})
	if len(cf.dns.Records()) != 1 {
		t.Error("adopted record deleted")
	}
}

func TestDeleteReportsDNSFailure(t *testing.T) {
	s, cf, d := newDNSServer(t)
	r := addRoute(t, s, d)
	cf.dns.FailWith("nope")
	if w := s.do(req{method: "DELETE", path: "/routes/" + itoa(r.ID) + "?delete_dns=on", hdr: htmxPost}); w.Code != 204 {
		t.Fatalf("route delete must still succeed: %d", w.Code)
	}
	if body := s.do(req{path: "/"}).Body.String(); !strings.Contains(body, "could not be removed") {
		t.Errorf("flash missing: %s", body)
	}
	if body := s.do(req{path: "/"}).Body.String(); strings.Contains(body, "could not be removed") {
		t.Error("flash shown twice")
	}
}

func TestRenameMovesRecord(t *testing.T) {
	s, cf, d := newDNSServer(t)
	r := addRoute(t, s, d)
	e := form("domain_id", itoa(d.ID), "subdomain", "tv", "enabled", "on")
	if w := s.do(req{method: "PUT", path: "/routes/" + itoa(r.ID), body: e.Encode(), hdr: htmxPost}); w.Code != 200 {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	recs := cf.dns.Records()
	if len(recs) != 1 || recs[0].Name != "tv.example.com" {
		t.Fatalf("%+v", recs)
	}
}

func TestDeleteRecordOnlyWhenAskedAndOwned(t *testing.T) {
	del := func(s *Server, id int64, q string) {
		t.Helper()
		if w := s.do(req{method: "DELETE", path: "/routes/" + itoa(id) + q, hdr: htmxPost}); w.Code != 204 {
			t.Fatalf("delete: %d %q", w.Code, w.Body.String())
		}
	}
	// owned, not asked: the route goes, the record stays
	s, cf, d := newDNSServer(t)
	del(s, addRoute(t, s, d).ID, "")
	if len(cf.dns.Records()) != 1 {
		t.Error("record deleted without being asked")
	}
	// owned, asked: both go
	s, cf, d = newDNSServer(t)
	del(s, addRoute(t, s, d).ID, "?delete_dns=on")
	if len(cf.dns.Records()) != 0 {
		t.Error("asked record not deleted")
	}
	// adopted, asked: never deleted
	s, cf, d = newDNSServer(t)
	cf.dns.Seed("sonarr.example.com", "10.0.0.1")
	del(s, addRoute(t, s, d).ID, "?delete_dns=on")
	if len(cf.dns.Records()) != 1 {
		t.Error("adopted record deleted")
	}
}

func TestListRowDeletePanel(t *testing.T) {
	// One dialog for the whole page, outside the polled #routes fragment so a refresh cannot close it.
	s, _, d := newDNSServer(t)
	addRoute(t, s, d)
	body := s.do(req{path: "/"}).Body.String()
	if strings.Count(body, "<dialog") != 1 || !strings.Contains(body, `id="delete-dialog"`) || !strings.Contains(body, `name="delete_dns"`) {
		t.Errorf("want one shared dialog with the DNS option:\n%s", body)
	}
	if !strings.Contains(body, `data-host="sonarr.example.com"`) || !strings.Contains(body, `data-dns-ip="10.0.0.1"`) {
		t.Errorf("owned row button lacks its data:\n%s", body)
	}
	if frag := s.do(req{path: "/status"}).Body.String(); strings.Contains(frag, "<dialog") || !strings.Contains(frag, `data-dns-ip="10.0.0.1"`) {
		t.Errorf("polled fragment must carry row data but no dialog:\n%s", frag)
	}
	s, cf, d := newDNSServer(t)
	cf.dns.Seed("sonarr.example.com", "10.0.0.1")
	addRoute(t, s, d)
	if body = s.do(req{path: "/"}).Body.String(); strings.Contains(body, "data-dns-ip") {
		t.Errorf("adopted row must not offer the record for deletion:\n%s", body)
	}
}

// Row actions are icon-only: each needs an accessible name and a tooltip, or it is an unlabeled mystery button.
func TestRowActionsAreLabelledIcons(t *testing.T) {
	s, cf, d := newDNSServer(t)
	addRoute(t, s, d)
	cf.dns.FailWith("down")
	s.do(req{method: "POST", path: "/routes/1/dns/resync", hdr: htmxPost}) // row ends in DNS error: Retry button
	body := s.do(req{path: "/status"}).Body.String()
	for _, label := range []string{"Edit", "Disable", "Delete", "Retry"} {
		if !strings.Contains(body, `aria-label="`+label+`"`) {
			t.Errorf("no icon button labelled %q:\n%s", label, body)
		}
	}
	if strings.Count(body, "<svg") < 4 || strings.Count(body, "data-tooltip=") < 4 {
		t.Errorf("icons or tooltips missing:\n%s", body)
	}
	if strings.Contains(body, ">Edit</a>") || strings.Contains(body, ">Disable</button>") {
		t.Errorf("text labels still shown:\n%s", body)
	}
}
