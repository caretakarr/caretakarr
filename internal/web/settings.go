// The Settings page: app-level email delivery for magic-link site access.
// Secrets: SMTP_PASS is sealed with secretbox (CARETAKARR_SECRET_KEY) and never logged.
package web

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/caretakarr/caretakarr/internal/gatelogic"
	"github.com/caretakarr/caretakarr/internal/secretbox"
	"github.com/caretakarr/caretakarr/internal/store"
)

type smtpData struct {
	Host      string
	Port      string // text for the input's value
	User      string
	From      string
	PassSet   bool // a sealed password exists
	MailReady bool // host and sender set: the gate can send
}

func (s *Server) settingsData(w http.ResponseWriter, r *http.Request) (smtpData, bool) {
	st, err := s.deps.Store.GetSettings(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return smtpData{}, false
	}
	return smtpData{Host: st.SMTPHost, User: st.SMTPUser, From: st.MailFrom,
		PassSet: st.SMTPPass != nil, MailReady: st.MailReady()}, true
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	d, ok := s.settingsData(w, r)
	if !ok {
		return
	}
	s.render(w, "settings", http.StatusOK, page{Title: "Settings", Data: d})
}

// saveSMTP replaces the email delivery settings wholesale; a blank password keeps the stored one.
func (s *Server) saveSMTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	host, user := strings.TrimSpace(r.PostFormValue("smtp_host")), strings.TrimSpace(r.PostFormValue("smtp_user"))
	from := strings.TrimSpace(r.PostFormValue("mail_from"))
	var err error
	if from != "" {
		if from, err = gatelogic.NormalizeEmail(from); err != nil {
			fail(w, http.StatusUnprocessableEntity, "MAIL FROM: "+err.Error())
			return
		}
	}
	port := 587
	if s := strings.TrimSpace(r.PostFormValue("smtp_port")); s != "" {
		if port, err = strconv.Atoi(s); err != nil || port < 1 || port > 65535 {
			fail(w, http.StatusUnprocessableEntity, "SMTP port "+s+" must be 1-65535")
			return
		}
	}
	if (host == "") != (from == "") {
		fail(w, http.StatusUnprocessableEntity, "Email delivery needs both an SMTP host and a MAIL FROM address; clear both to stop sending.")
		return
	}
	cur, err := s.deps.Store.GetSettings(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	pw := r.PostFormValue("smtp_pass")
	var pass []byte
	if host != "" {
		if pw == "" {
			pass = cur.SMTPPass // keep
		} else if s.deps.Cfg.SecretKey == "" {
			fail(w, http.StatusUnprocessableEntity, "Storing an SMTP password needs CARETAKARR_SECRET_KEY (at least 32 characters) in .env. Leave the password blank for a relay that does not ask for one.")
			return
		} else if len(pw) > 1024 {
			fail(w, http.StatusUnprocessableEntity, "SMTP password is too long")
			return
		} else if pass, err = secretbox.Seal(s.deps.Cfg.SecretKey, []byte(pw)); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.deps.Store.SaveSettings(r.Context(), store.Settings{
		SMTPHost: host, SMTPPort: port, SMTPUser: user, SMTPPass: pass, MailFrom: from,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("settings saved", "event", "settings.smtp", "ready", host != "")
	hxRedirect(w, "/settings")
}
