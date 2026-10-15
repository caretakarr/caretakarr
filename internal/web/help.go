package web

import (
	"fmt"
	"html"
	"html/template"
	"strings"
	"time"

	"github.com/caretakarr/caretakarr/internal/config"
)

// All user-facing help lives here so form hints, status tooltips, error explanations and the /help
// page share one source. A test fails if a template references a hint or help section that is missing.

// hints are one-line, plain-text field explanations (rendered by the hint template func).
var hints = map[string]string{
	"domain":        "The Cloudflare zone your sites live in, such as example.com. Add it once; sites like oc.example.com go under it.",
	"default_ip":    "The address your sites' DNS records point to: this server, the machine running Caretakarr. It must be a private address (192.168.x.x, 10.x.x.x, 172.16-31.x.x or 100.64-127.x.x).",
	"cert_mode":     "Wildcard gets one *.example.com certificate for every site, so adding sites is instant and site names stay private. Per host gets a separate certificate for each site.",
	"hostname":      "The address people type, such as oc.example.com. Caretakarr finds its Cloudflare zone and adds it if it is new.",
	"preset":        "Fills in the usual port for this app. You can still change it.",
	"scheme":        "How Caddy talks to your app. Use http unless the app itself serves HTTPS.",
	"upstream_host": "Where the app really runs: an IP address such as 192.168.25.165, or a Docker container name on a shared network. Not the public hostname.",
	"upstream_port": "The port the app listens on, such as 9200 (Sonarr is 8989, Radarr 7878). Not 443: Caddy already provides HTTPS in front of it.",
	"skip_verify":   "Only for apps that use https with a self-signed certificate. Turns off checking that certificate. Fine on a trusted home network.",
	"stream":        "Sends the app's answer to the visitor as it arrives instead of in chunks. Turn on for live streams, event feeds and terminals that seem to freeze or lag. Websockets work without it.",
	"max_body":      "The biggest upload this site accepts, such as 2GB or 512MB (up to 100GB). Larger uploads get error 413. Leave empty for no limit.",
	"subdomain":     "The first part of the address, such as oc in oc.example.com. Letters, numbers and hyphens only.",
	"record_ip":     "Leave blank to use the domain's default IP. Set it only if this one site's DNS record must point at a different server.",
	"allowlist":     "Only these networks can open the site; everyone else gets an error 403. One per line. Leave empty to allow anyone who can reach this server. Example: 192.168.0.0/16 is a typical whole home network.",
	"basic_auth":    "Puts a username and password prompt in front of the app. Leave the user empty for no prompt.",
	"enabled":       "Turn off to stop serving the site without deleting it. Caddy ignores it completely, so no certificate work happens for it. Its DNS record is kept.",
	"admin_login":   "The password set as ADMIN_PASSWORD when Caretakarr was started.",
	"totp_code":     "The 6-digit code from your authenticator app. A backup code also works, once.",
	"backup_codes":  "Each code works once if you lose your authenticator. Keep them somewhere safe; they are not shown again.",
	"passkey_name":  "A label so you can tell your passkeys apart, such as Laptop or Phone.",
	"secret_key":    "Authenticator codes are stored encrypted. Set CARETAKARR_SECRET_KEY (at least 32 characters, for example from openssl rand -hex 32) in .env and recreate the container to turn them on. Back it up with your data volume.",
	"gate_mode":     "A magic link (sent by email, valid for 15 minutes and single-use) opens the site only after it is clicked. With the allowlist, only listed emails and @domain rules get a link; with any email, anyone with a working inbox can ask for one.",
	"gate_acl":      "One rule per line: alice@example.com admits exactly that address; @contractor.example admits everyone at that domain. Rules are matched after lowering case; a session is shared between all gated sites that allow the address.",
	"smtp_host":     "The submission server your provider gives you, such as smtp.fastmail.com. Leave empty (with MAIL FROM) to stop sites from sending.",
	"smtp_port":     "587 (STARTTLS submission) for most providers.",
	"smtp_pass":     "Stored encrypted with CARETAKARR_SECRET_KEY and never shown again. Blank for relays that do not ask for one.",
	"mail_from":     "The sender address on the magic-link emails, such as access@example.com. Must be allowed to send from your provider.",
	"smtp_setup":    "Set the SMTP host and MAIL FROM under Settings. A password also needs CARETAKARR_SECRET_KEY in .env.",
	"apply":         "Saving applies the change at once: Caddy validates it before acceptance, so a rejection takes nothing down.",
}

// statusHelp explains every status word and what to do next. Keys are kind:state.
type statusInfo struct{ Label, Meaning, Next string }

var statusOrder = []string{"cert:issued", "cert:pending", "cert:expiring", "cert:error", "dns:synced", "dns:pending", "dns:conflict", "dns:error", "up:up", "up:down"}

var statusHelp = map[string]statusInfo{
	"cert:issued":   {"Certificate issued", "A valid HTTPS certificate is being served for this site.", ""},
	"cert:pending":  {"Certificate pending", "Caretakarr is still getting the certificate from Let's Encrypt. It proves it owns the domain by writing a temporary DNS record and waiting for Cloudflare to publish it.", "Wait; the first one can take several minutes. Watch the Logs page if it takes longer."},
	"cert:expiring": {"Certificate expiring soon", "The certificate expires in less than three weeks. Caddy normally renews it a month early, so renewal is overdue or failing.", "Open the Logs page and look for certificate errors."},
	"cert:error":    {"Certificate error", "Getting or serving the certificate failed.", "Read the explanation shown with it, then check the Logs page."},
	"dns:synced":    {"DNS in sync", "The A record in Cloudflare exists and points where this site expects.", ""},
	"dns:pending":   {"DNS not yet synced", "Caretakarr has not created or checked this site's A record yet.", "Press Resync."},
	"dns:conflict":  {"DNS conflict", "An A record with this name already exists and points somewhere else. Caretakarr did not touch it.", "Press Take over to replace it, or fix it in Cloudflare and press Resync."},
	"dns:error":     {"DNS error", "Talking to Cloudflare failed. The site itself still works if its record already exists.", "Read the explanation shown with it, then press Retry."},
	"up:up":         {"Backend reachable", "Caretakarr can open a connection to the app's address and port.", ""},
	"up:down":       {"Backend unreachable", "Nothing answered at the app's address and port, so visitors will get an error.", "Check the app is running, and that the host and port are right and reachable from this server."},
}

// explained is a plain-language version of a known technical error.
type explained struct{ Title, Fix string }

var errTable = []struct {
	match string // lower-case substring
	explained
}{
	{"timed out waiting for record to fully propagate", explained{"Cloudflare was slow to publish the verification record",
		"This is common and Caretakarr keeps retrying. If it never succeeds, raise ACME_PROPAGATION_TIMEOUT (and ACME_PROPAGATION_DELAY) in .env and recreate the container."}},
	{"no cloudflare zone found", explained{"That domain's zone was not found in Cloudflare",
		"Check the spelling, that the domain is added to your Cloudflare account, and that the API token can read that zone."}},
	{"the token cannot see it", explained{"That domain's zone was not found in Cloudflare",
		"Check the spelling, that the domain is added to your Cloudflare account, and that the API token can read that zone."}},
	{"cannot be decrypted", explained{"The saved authenticator cannot be opened",
		"CARETAKARR_SECRET_KEY is missing or different from when it was set up. Restore the old key, use a backup code, or run caretakarr 2fa-reset."}},
	{"multiple a records", explained{"More than one A record has this name",
		"Delete the extra records in Cloudflare so only one is left, then press Resync."}},
	{"invalid access token", explained{"Cloudflare rejected the API token",
		"Create a new token with Zone:Read and DNS:Edit, put it in .env as CLOUDFLARE_API_TOKEN, and recreate the container."}},
	{"verify cloudflare token", explained{"Cloudflare rejected the API token",
		"Create a new token with Zone:Read and DNS:Edit, put it in .env as CLOUDFLARE_API_TOKEN, and recreate the container."}},
	{"must be a private ipv4", explained{"Only private addresses are allowed",
		"Use an address from your own network: 192.168.x.x, 10.x.x.x, 172.16-31.x.x, or 100.64-127.x.x (Tailscale)."}},
	{"is the caretakarr ui host", explained{"That name is Caretakarr's own address",
		"Choose a different subdomain for this site."}},
	{"is a zone itself", explained{"Use a subdomain, not the domain itself",
		"Use a subdomain such as app.example.com instead of example.com itself."}},
	{"more than one label below", explained{"The name is more than one level below the domain",
		"A wildcard certificate only covers one level. Use name.example.com, or add the deeper zone as its own domain."}},
	{"caddy rejected config", explained{"Caddy rejected the configuration",
		"Nothing was changed. The detail below says which setting Caddy did not accept."}},
	{"ratelimited", explained{"Let's Encrypt rate limit reached",
		"Too many certificates were requested recently. Wait before retrying, or set ACME_STAGING=true while testing."}},
	{"too many certificates", explained{"Let's Encrypt rate limit reached",
		"Too many certificates were requested recently. Wait before retrying, or set ACME_STAGING=true while testing."}},
	{"context canceled", explained{"The attempt was interrupted",
		"This usually happens during a restart. It retries automatically."}},
	{"already exists on that domain", explained{"That site already exists",
		"Open it from the dashboard to change it, or choose a different name."}},
}

// explain returns a plain-language explanation for known errors only; unknown errors are shown raw.
func explain(msg string) (explained, bool) {
	l := strings.ToLower(msg)
	for _, e := range errTable {
		if strings.Contains(l, e.match) {
			return e.explained, true
		}
	}
	return explained{}, false
}

// errorHTML renders msg for display: the plain-language explanation first (if known), the raw text always.
func errorHTML(msg string) string {
	raw := html.EscapeString(msg)
	e, ok := explain(msg)
	if !ok {
		return raw
	}
	return "<strong>" + html.EscapeString(e.Title) + ".</strong> " + html.EscapeString(e.Fix) + "<br><small>" + raw + "</small>"
}

// certWait is the longest Caddy waits for one DNS-01 attempt: the delay before checking plus the timeout.
func certWait(delay, timeout time.Duration) string {
	if timeout == 0 {
		timeout = 2 * time.Minute // Caddy's default
	}
	mins := int((delay + timeout).Round(time.Minute) / time.Minute)
	return fmt.Sprintf("The first certificate can take up to %d minutes. It retries on its own; the Logs page shows progress.", mins)
}

type helpSection struct {
	ID, Title string
	Body      template.HTML // trusted static markup
}

var helpSections = []helpSection{
	{"how-it-works", "How Caretakarr works", `
<p>Caretakarr puts your apps (Sonarr, Radarr, Plex and so on) behind real web addresses with valid HTTPS. For each <strong>site</strong>, such as <code>oc.example.com</code>, it does three things:</p>
<ol>
<li>Creates a <strong>DNS record</strong> in Cloudflare so the name points to this server.</li>
<li>Gets an <strong>HTTPS certificate</strong> from Let's Encrypt. It proves it owns the domain by writing a temporary record in Cloudflare, so this server never needs to be reachable from the internet.</li>
<li>Tells <strong>Caddy</strong> (the web server inside Caretakarr) to forward requests for that name to your app's <strong>backend</strong>: its IP address and port, such as <code>192.168.25.165:9200</code>.</li>
</ol>
<p>Every change is checked by Caddy before it goes live. If Caddy rejects it, nothing changes and you see the reason.</p>`},
	{"sites", "Sites, domains and ports", `
<p>A <strong>domain</strong> is the Cloudflare zone, such as <code>example.com</code>. A <strong>site</strong> is one address under it, such as <code>oc.example.com</code>. Use <strong>Add site</strong>: it finds the zone and adds the domain for you the first time.</p>
<p>The <strong>backend port</strong> is the port your app listens on and differs for every app. It is not 443; Caddy provides HTTPS in front of it. The DNS record points at this server, not at the backend, so the backend can be on another machine.</p>`},
	{"sign-in", "Sign-in security", `
<p>Under <strong>Security</strong> you can add an <strong>authenticator app</strong> (a 6-digit code after the password) and <strong>passkeys</strong> (sign in with your fingerprint, face or device PIN instead of the password). A passkey sign-in counts as full two-factor, so it asks for no code. Passkeys work only on the HTTPS address, or on <code>localhost</code>.</p>
<p>If you lose your authenticator and passkeys, use a backup code. If those are gone too, run <code>docker compose run --rm caretakarr caretakarr 2fa-reset</code>: it removes all of them and the password works alone again.</p>
<p>Setting <code>TURNSTILE_SITE_KEY</code> and <code>TURNSTILE_SECRET</code> adds a Cloudflare Turnstile check to the password form on the HTTPS address. The recovery address (<code>UI_LISTEN</code>) skips it.</p>`},
	{"certificates", "Certificates: wildcard or per host", `
<p><strong>Wildcard</strong> (the default) gets one certificate for <code>*.example.com</code> that covers every site under the domain. New sites are instant, and the names of your sites stay out of public certificate logs.</p>
<p><strong>Per host</strong> gets a separate certificate for each site, which is slower for each new site. A wildcard covers one level only: <code>oc.example.com</code> yes, <code>a.oc.example.com</code> no.</p>`},
	{"statuses", "What the status words mean", ``}, // table is generated from statusHelp
	{"token", "The Cloudflare token", `
<p>Caretakarr needs one Cloudflare API token with two permissions on your zones: <strong>Zone &rarr; Zone &rarr; Read</strong> and <strong>Zone &rarr; DNS &rarr; Edit</strong>. Create it under My Profile, API Tokens. It is set as <code>CLOUDFLARE_API_TOKEN</code> in <code>.env</code> and is never stored in the database or shown here. After changing <code>.env</code>, recreate the container: <code>docker compose up -d --force-recreate</code>.</p>`},
	{"lan-only", "Private addresses only", `
<p>Caretakarr only publishes <strong>private</strong> addresses: 10.x.x.x, 172.16-31.x.x, 192.168.x.x, and 100.64-127.x.x (Tailscale). Your apps are reachable from your home network or tailnet, not from the open internet. Public addresses and IPv6 are not supported.</p>`},
	{"access", "Limiting who can open a site", `
<p>On a site's edit page you can limit it to certain networks (<strong>Allowed sources</strong>, for example <code>192.168.0.0/16</code> for your whole home network) and add a <strong>username and password</strong>. Everyone else gets an error. The password is stored only as a hash.</p>
<p><strong>Magic-link access</strong> goes one step further: after the allowlist (if you use one), the site asks for an email address and sends a single-use link. Sessions last 30 days with an allowlist, 7 days with any email, sliding: activity extends them. Revoke one identity under <strong>Security</strong> and its access dies on every gated site at once.</p>`},
	{"settings", "Settings: email delivery", `
<p>Email delivery lives on <strong>Settings</strong> page: an <strong>SMTP host</strong> (usually port 587), optionally a <strong>user and password</strong>, and the <strong>MAIL FROM</strong> address the magic links are sent from. The password is stored encrypted; <code>CARETAKARR_SECRET_KEY</code> still comes from .env.</p>
<p>Saving a host and MAIL FROM turns magic-link access on at once: gated sites send on their next request, no recreate needed. Clearing both stops it.</p>`},
	{"troubleshooting", "Troubleshooting", ``}, // list is generated from errTable
}

// helpPage is the data for /help.
type helpPage struct {
	Sections []helpSection
	Statuses []statusInfo
	Errors   []explained
}

func newHelpPage() helpPage {
	p := helpPage{Sections: helpSections}
	for _, k := range statusOrder {
		p.Statuses = append(p.Statuses, statusHelp[k])
	}
	seen := map[string]bool{}
	for _, e := range errTable {
		if !seen[e.Title] {
			seen[e.Title] = true
			p.Errors = append(p.Errors, e.explained)
		}
	}
	return p
}

// helpFuncs are the template functions that render help consistently.
var helpFuncs = template.FuncMap{
	"hint": func(key string) template.HTML {
		return template.HTML("<small>" + html.EscapeString(hints[key]) + "</small>") // #nosec: escaped above
	},
	"helplink": func(anchor string) template.HTML {
		return template.HTML(`<a class="help-link" href="/help#` + html.EscapeString(anchor) + `" title="Open help" aria-label="Open help">?</a>`)
	},
	"size":   config.FormatSize,
	"icon":   icon,
	"status": func(kind, state string) statusInfo { return statusHelp[kind+":"+state] },
	"errhtml": func(msg string) template.HTML {
		return template.HTML(errorHTML(msg)) // #nosec: errorHTML escapes every piece
	},
}
