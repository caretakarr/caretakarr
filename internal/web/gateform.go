// Gate form helpers: the protection block on site forms and its validation, reading the
// settings row (not env) to know whether email delivery works.
package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/caretakarr/caretakarr/internal/gatelogic"
	"github.com/caretakarr/caretakarr/internal/store"
)

// mailReady reports whether magic links can be sent right now.
func (s *Server) mailReady(ctx context.Context) (bool, error) {
	set, err := s.deps.Store.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	return set.MailReady(), nil
}

// parseGate reads the route form's protection block. Gate-off clears the grants; other modes
// need email delivery and the session key configured, and acl needs at least one rule.
func (s *Server) parseGate(ctx context.Context, rt store.Route, v url.Values) ([]store.Grant, error) {
	if err := s.gateNeedsHelp(ctx, rt); err != nil {
		return nil, err
	}
	if rt.GateMode == "" || rt.GateMode == "open" {
		return []store.Grant{}, nil
	}
	rules, err := gatelogic.ParseACL(v.Get("gate_acl"))
	if err != nil {
		return nil, userError{http.StatusUnprocessableEntity, err.Error()}
	}
	if len(rules) == 0 {
		return nil, userError{http.StatusUnprocessableEntity, "Add an address or an @domain to the allowlist."}
	}
	gs := make([]store.Grant, len(rules))
	for i, r := range rules {
		gs[i] = store.Grant{Mode: r.Mode, Value: r.Value}
	}
	return gs, nil
}

// gateACLText is the textarea content for an existing route's allowlist.
func gateACLText(gs []store.Grant) string {
	lines := make([]string, 0, len(gs))
	for _, g := range gs {
		if g.Mode == "domain" {
			lines = append(lines, "@"+g.Value)
		} else {
			lines = append(lines, g.Value)
		}
	}
	return strings.Join(lines, "\n")
}

// gateNeedsHelp refuses a gate when the app cannot honor it (checked together on both forms).
func (s *Server) gateNeedsHelp(ctx context.Context, rt store.Route) error {
	if rt.GateMode == "" {
		return nil
	}
	ok, err := s.mailReady(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return userError{http.StatusUnprocessableEntity, "Magic-link protection needs email delivery: set the SMTP host and MAIL FROM under Settings."}
	}
	if s.deps.Cfg.SecretKey == "" {
		return userError{http.StatusUnprocessableEntity, fmt.Sprintf("Magic-link sessions need CARETAKARR_SECRET_KEY (at least 32 characters) in .env to mint session keys for %s.", rt.Subdomain)}
	}
	return nil
}
