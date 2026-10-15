// Package config parses and validates environment configuration. Secrets live here only.
package config

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// cfToken mirrors caddy-dns/cloudflare's own check, which otherwise makes the whole Caddy config unloadable.
var cfToken = regexp.MustCompile(`^([A-Za-z0-9_-]{35,50}|cf(ut|at)_[A-Za-z0-9_-]{32,256})$`)

type Config struct {
	CloudflareToken    string
	Host               string
	HostIP             string // A record target for Host; private/CGNAT IPv4
	HostZone           string // optional Cloudflare zone name for Host; empty = walk up labels
	ACMEEmail          string
	AdminPassword      string
	Staging            bool
	PropagationDelay   time.Duration // 0 = unset
	PropagationTimeout time.Duration // 0 = unset
	Resolvers          []string
	UIListen           string
	DataDir            string
	DockerSocket       string // optional discovery; the panel is hidden when this path does not exist
	CFBaseURL          string // test hook: point cloudflare-go at a mock (e2e); empty = real API
	HTTPPort           int    // Caddy listen ports; default 80/443, overridable for unprivileged runs
	HTTPSPort          int
	SecretKey          string // seals TOTP seeds at rest; empty = TOTP enrollment off
	Require2FA         bool
	TurnstileSiteKey   string // both set = Turnstile on the password step
	TurnstileSecret    string
	TurnstileBaseURL   string // test hook: siteverify endpoint override; empty = Cloudflare
	// Site access gate (§18). Email delivery lives in the Settings page in SQLite, password
	// secretbox-sealed; no SMTP_* env vars are read. Secrets stay env-only: CARETAKARR_SECRET_KEY.
	// ACME_ISSUER=internal switches the whole render to Caddy's internal CA (self-signed):
	// used by e2e and offline setups; default "acme" is the production path.
	Issuer string
}

// Load reads configuration through getenv (os.Getenv in production) and reports every problem at once.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	req := func(k string) string {
		v := strings.TrimSpace(getenv(k))
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", k))
		}
		return v
	}
	opt := func(k, def string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return def
	}
	dur := func(k string) time.Duration {
		v := strings.TrimSpace(getenv(k))
		if v == "" {
			return 0
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s: %q is not a positive duration", k, v))
			return 0
		}
		return d
	}

	port := func(k string, def int) int {
		v := strings.TrimSpace(getenv(k))
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			errs = append(errs, fmt.Errorf("%s: %q is not a port", k, v))
			return def
		}
		return n
	}

	c := Config{
		DockerSocket:       opt("DOCKER_SOCKET", "/var/run/docker.sock"),
		CFBaseURL:          strings.TrimSpace(getenv("CLOUDFLARE_BASE_URL")),
		HTTPPort:           port("HTTP_PORT", 80),
		HTTPSPort:          port("HTTPS_PORT", 443),
		CloudflareToken:    req("CLOUDFLARE_API_TOKEN"),
		Host:               req("CARETAKARR_HOST"),
		HostIP:             req("CARETAKARR_HOST_IP"),
		HostZone:           strings.ToLower(strings.TrimSpace(getenv("CARETAKARR_HOST_ZONE"))),
		ACMEEmail:          req("ACME_EMAIL"),
		AdminPassword:      getenv("ADMIN_PASSWORD"),
		PropagationDelay:   dur("ACME_PROPAGATION_DELAY"),
		PropagationTimeout: dur("ACME_PROPAGATION_TIMEOUT"),
		UIListen:           opt("UI_LISTEN", "127.0.0.1:8080"),
		DataDir:            opt("DATA_DIR", "/data"),
	}
	for _, r := range strings.Split(opt("ACME_RESOLVERS", "1.1.1.1,8.8.8.8"), ",") {
		if r = strings.TrimSpace(r); r != "" {
			c.Resolvers = append(c.Resolvers, r)
		}
	}
	if v := strings.TrimSpace(getenv("ACME_STAGING")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("ACME_STAGING: %q is not a boolean", v))
		}
		c.Staging = b
	}
	c.SecretKey = getenv("CARETAKARR_SECRET_KEY")
	c.TurnstileSiteKey = strings.TrimSpace(getenv("TURNSTILE_SITE_KEY"))
	c.TurnstileSecret = strings.TrimSpace(getenv("TURNSTILE_SECRET"))
	c.TurnstileBaseURL = strings.TrimSpace(getenv("TURNSTILE_BASE_URL"))
	c.Issuer = strings.ToLower(strings.TrimSpace(getenv("ACME_ISSUER")))
	if v := strings.TrimSpace(getenv("REQUIRE_2FA")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("REQUIRE_2FA: %q is not a boolean", v))
		}
		c.Require2FA = b
	}
	if c.SecretKey != "" && len(c.SecretKey) < 32 {
		errs = append(errs, errors.New("CARETAKARR_SECRET_KEY: must be at least 32 characters (try: openssl rand -hex 32)"))
	}
	if c.Require2FA && c.AdminPassword == "" {
		errs = append(errs, errors.New("REQUIRE_2FA needs ADMIN_PASSWORD"))
	}
	if c.Require2FA && c.SecretKey == "" {
		errs = append(errs, errors.New("REQUIRE_2FA needs CARETAKARR_SECRET_KEY"))
	}
	if (c.TurnstileSiteKey == "") != (c.TurnstileSecret == "") {
		if c.TurnstileSiteKey == "" {
			errs = append(errs, errors.New("TURNSTILE_SITE_KEY is required when TURNSTILE_SECRET is set"))
		} else {
			errs = append(errs, errors.New("TURNSTILE_SECRET is required when TURNSTILE_SITE_KEY is set"))
		}
	}
	if c.CloudflareToken != "" && !cfToken.MatchString(c.CloudflareToken) {
		errs = append(errs, errors.New("CLOUDFLARE_API_TOKEN: not a valid Cloudflare API token format (no quotes or braces)"))
	}
	if c.Host != "" {
		if err := ValidateDomain(c.Host); err != nil {
			errs = append(errs, fmt.Errorf("CARETAKARR_HOST: %w", err))
		}
	}
	if c.HostIP != "" {
		if _, err := ValidateLANIP(c.HostIP); err != nil {
			errs = append(errs, fmt.Errorf("CARETAKARR_HOST_IP: %w", err))
		}
	}
	if c.HostZone != "" && c.Host != "" && c.Host != c.HostZone && !strings.HasSuffix(c.Host, "."+c.HostZone) || c.HostZone != "" && !strings.Contains(c.HostZone, ".") {
		errs = append(errs, fmt.Errorf("CARETAKARR_HOST_ZONE: %q must be a zone containing %q (at least two labels)", c.HostZone, c.Host))
	}
	if c.ACMEEmail != "" {
		// Bare address only: ParseAddress also accepts "Name <a@b>", which Caddy would pass through verbatim.
		if a, err := mail.ParseAddress(c.ACMEEmail); err != nil || a.Address != c.ACMEEmail {
			errs = append(errs, fmt.Errorf("ACME_EMAIL: %q is not a bare email address", c.ACMEEmail))
		}
	}
	if c.Issuer != "" && c.Issuer != "acme" && c.Issuer != "internal" {
		errs = append(errs, fmt.Errorf("ACME_ISSUER: %q is acme or internal", c.Issuer))
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return c, nil
}
