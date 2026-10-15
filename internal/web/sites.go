package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/caretakarr/caretakarr/internal/config"
	"github.com/caretakarr/caretakarr/internal/discovery"
	"github.com/caretakarr/caretakarr/internal/presets"
	"github.com/caretakarr/caretakarr/internal/store"
)

// dockerPanel is the opt-in container list shown on both route-creation pages.
type dockerPanel struct {
	Show       bool // the Docker socket is mounted
	Err        string
	Containers []discovery.Container
}

func (s *Server) dockerPanel(r *http.Request) dockerPanel {
	if s.deps.Docker == nil || !s.deps.Docker.Available() {
		return dockerPanel{}
	}
	p := dockerPanel{Show: true}
	var err error
	if p.Containers, err = s.deps.Docker.List(r.Context()); err != nil {
		p.Err = err.Error()
	}
	return p
}

type addSiteData struct {
	Hostname, Host string
	Port           int
	ServerIP       string // where the A record will point; shown in the live preview
	Docker         dockerPanel
	Presets        []presets.Preset
	MailOK         bool // email delivery works: show the magic-link fieldset active
}

// newSitePage supports prefill from the Docker panel via ?host=&port=.
func (s *Server) newSitePage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	port, _ := strconv.Atoi(q.Get("port"))
	mail, merr := s.mailReady(r.Context())
	if merr != nil {
		slog.Error("gate mail settings", "error", merr)
	}
	s.render(w, "site", http.StatusOK, page{Title: "Add site", Data: addSiteData{
		Hostname: q.Get("hostname"), Host: q.Get("host"), Port: port, ServerIP: s.deps.Cfg.HostIP,
		Docker: s.dockerPanel(r), Presets: presets.All, MailOK: mail && s.deps.Cfg.SecretKey != ""}})
}

// addSite turns "oc.example.com -> 192.168.25.165:9200" into the managed domain (if new), the route and its A record.
// The backend address and port are separate fields, so the hostname/zone logic never has to parse a host:port.
func (s *Server) addSite(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	hostname := strings.ToLower(strings.TrimSpace(r.PostFormValue("hostname")))
	if err := config.ValidateDomain(hostname); err != nil {
		fail(w, http.StatusUnprocessableEntity, "hostname: "+err.Error())
		return
	}
	v := r.PostForm
	v.Set("domain_id", "0") // placeholders: parseRoute validates the rest; the real values are resolved below
	v.Set("subdomain", strings.SplitN(hostname, ".", 2)[0])
	rt, err := parseRoute(v)
	if err == nil {
		err = applyControls(&rt, v)
	}
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	domains, err := s.deps.Store.ListDomains(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	var managed *store.Domain // the longest managed domain that encloses the hostname
	for i, d := range domains {
		if strings.HasSuffix(hostname, "."+d.Name) && (managed == nil || len(d.Name) > len(managed.Name)) {
			managed = &domains[i]
		}
	}
	var newDomain store.Domain
	zoneName := ""
	if managed != nil {
		zoneName = managed.Name
	} else {
		if _, err := config.ValidateLANIP(s.deps.Cfg.HostIP); err != nil {
			fail(w, http.StatusUnprocessableEntity, "CARETAKARR_HOST_IP: "+err.Error())
			return
		}
		zoneID, zone, err := s.deps.CF.ZoneFor(r.Context(), hostname, "")
		if err != nil {
			fail(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		zoneName = zone
		newDomain = store.Domain{Name: zone, ZoneID: zoneID, DefaultIP: s.deps.Cfg.HostIP, CertMode: "wildcard"}
	}
	if hostname == zoneName {
		fail(w, http.StatusUnprocessableEntity, fmt.Sprintf("%s is a zone itself; use a subdomain such as app.%s", hostname, zoneName))
		return
	}
	rt.Subdomain = strings.TrimSuffix(hostname, "."+zoneName)
	if strings.Contains(rt.Subdomain, ".") {
		fail(w, http.StatusUnprocessableEntity, fmt.Sprintf("%s is more than one label below %s; a wildcard certificate would not cover it. Use name.%s", hostname, zoneName, zoneName))
		return
	}

	grants, err := s.parseGate(r.Context(), rt, v)
	if err != nil {
		failErr(w, err)
		return
	}

	var created store.Route
	err = s.deps.Apply.Mutate(r.Context(), func(tx *store.Store) error {
		if managed != nil {
			rt.DomainID = managed.ID
		} else {
			d, err := tx.CreateDomain(r.Context(), newDomain)
			if err != nil {
				return err
			}
			rt.DomainID = d.ID
		}
		if err := s.checkRoute(r, tx, rt); err != nil {
			return err
		}
		var cerr error
		created, cerr = tx.CreateRoute(r.Context(), rt)
		if cerr != nil {
			return cerr
		}
		return tx.ReplaceGrants(r.Context(), created.ID, grants)
	})
	if err != nil {
		failErr(w, err)
		return
	}
	s.reconcile(r, created.ID)
	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusNoContent)
}
