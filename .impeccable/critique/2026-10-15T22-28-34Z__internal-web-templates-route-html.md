---
target: the edit site UI (Edit route form)
total_score: 22
max_score: 40
na_heuristics: 
p0_count: 1
p1_count: 3
target_identity: "file:/home/btafoya/projects/caretakarr/internal/web/templates/route.html"
target_fingerprint: "sha256:960c91e57f31cbe31585a4ce13ea09f31a5c19a0f06fa2e13a55eb344bf8c58e"
target_path: /home/btafoya/projects/caretakarr/internal/web/templates/route.html
timestamp: 2026-10-15T22-28-34Z
slug: internal-web-templates-route-html
---
Method: dual-agent
Target: internal/web/templates/route.html (the Edit site form), rendered by internal/web/web.go inside templates/layout.html; site.html/settings.html compared as siblings.

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|---|---|---|
| 1 | Visibility of System Status | 2 | No state anywhere: h1 says "Edit route" without naming the site; save success is a silent redirect; cert/DNS/backend chips live only on the dashboard you left |
| 2 | Match System and Real World | 2 | Vocabulary drift: surface says "route"/"Upstream", nav/dashboard/Add form say "site"/"Backend" — three words for the same pair of concepts |
| 3 | User Control and Freedom | 2 | Reversible changes, honest toggle, but no cancel, no dirty-state guard, and save discards your place |
| 4 | Consistency and Standards | 2 | Add vs Edit diverge (fieldset+grid+preview vs flat list); two delete patterns coexist (dashboard dialog with DNS choice vs native confirm with hardcoded delete_dns=on) |
| 5 | Error Prevention | 1 | A preset click on Edit silently overwrites the typed subdomain and port; the resulting rename later deletes the old Cloudflare A record. No preview, no dirty guard |
| 6 | Recognition Rather Than Recall | 2 | The full hostname is never assembled on screen; record_ip state (deliberate or default) must come from memory |
| 7 | Flexibility and Efficiency | 2 | The preset is the only accelerator and it misfires on Edit |
| 8 | Aesthetic and Minimalist Design | 2 | Clean tokens (no banned side-stripes/glows) but maximalist density: ~14 controls, ~14 hint paragraphs, all expanded |
| 9 | Error Recovery | 3 | Strong: plain-language explanations with raw text kept, "Nothing was changed." on revert, adaptive delete copy |
| 10 | Help and Documentation | 4 | The hint/helplink/help-page system with a failing test enforcing anchors is the standout |
| **Total** | | **22/40** | **Acceptable** |

## Design Specificity Verdict

The product voice is real (presets with true ports, adaptive delete copy, topology-citing hints), but the page leaks Caddy internals ("route") beside a deliberately plain layer, and unlike its sibling Add form, Edit never got an authoring pass: Add has a fieldset, a 3-column grid and a live preview; Edit is fourteen flat controls.

**Deterministic scan**: `impeccable detect` across route.html, site.html and settings.html returned zero findings (exit 0), verified against positive and negative controls (a slopped fixture produced 4 findings, so the engine was genuinely running). No false positives. Caveat: the templates carry no inline CSS — contrast/palette rules only see markup, so caretakarr.css is outside this scan's evidence. The detector cannot see (flagged for synthesis): the SMTP port field uses inputmode=numeric rather than type=number, aria/hint association gaps, and label misuse.

**Visual overlays**: browser step skipped — no viewable target is running, and standing up the full Caretakarr stack (env, Cloudflare mock, certificates) for an overlay pass was not justified. No user-visible overlay exists from this run.

## What's Working

1. The help layer (hint, helplink, adaptive confirm text, errTable) is best-in-class for a small admin UI, enforced by a test.
2. Dependency handling with an author's touch: the magic-link fieldset degrades into an actionable "set up email under Settings first" state instead of a dead picker; "leave blank to keep the current password" solves edit semantics in one sentence.
3. Coherent token-to-Pico mapping with visible design intent; zero design bans violated.

## Priority Issues

1. **[P0] The page does not say which site is being edited** — the h1 reads "Edit route" and no hostname appears anywhere, so every judgment (right site? what will this affect?) runs blind. Fix: render the host in the h1 or a context strip ("Edit oc.example.com") with the cert/DNS/backend chips the dashboard already computes. Suggested command: /impeccable layout.
2. **[P1] Preset silently overwrites the typed subdomain (and port) on Edit; a rename deletes the old Cloudflare A record** — stray click becomes data loss two actions later. Fix: on Edit fill the port always, the subdomain only when empty, and surface a pending-rename notice. Suggested command: /impeccable harden.
3. **[P1] No grouping or progressive disclosure** — ten bare controls before the first fieldset; access control split across three locations (magic-link fieldset, bare CIDR textarea, basic-auth fieldset); one-field edits read every hint. Fix: fieldsets with legends as site.html already does — Address, Backend (grid), Who can open this site (all three access controls in one place), Advanced in a native details block. Suggested command: /impeccable layout.
4. **[P1] Save feedback is asymmetric and unexplained** — success is a silent 204 + redirect to the dashboard; failure is one red paragraph at the page bottom; nothing says the change applies to live traffic immediately. Fix: inline "Saved and applied" on Edit instead of redirect, plus one line near Save stating the apply contract. Suggested command: /impeccable clarify.
5. **[P2] Hints are invisible to screen readers** — every hint is a sibling small element with no association; routes.html misuses a label wrapping a span with no control. Fix: emit hint ids and reference them via aria-describedby; make the gate-off state a paragraph. Suggested command: /impeccable harden.
6. **[P2] Edit-page delete is the cruder twin of the dashboard's delete dialog** — delete_dns=on hardcoded, native confirm instead of the dialog with the explicit DNS choice. Fix: reuse the dashboard dialog on Edit. Suggested command: /impeccable polish.

## Tab Verdict (the user's hypothesis)

Tabs are the wrong tool here, for stack reasons rather than taste: htmx holds no client state, so pane swapping re-renders from the server and destroys unsaved edits mid-form; hidden required fields behind a tab fight native validation (the browser blocks submit without a reason the user can see); and Pico has no tablist component to lean on. The cheaper correct fix already has half its pattern in the codebase: fieldset+legend+grid (site.html) plus native details (docker.html). The common visit is a single-field tweak — a wizard adds ceremony to serve the rare case. Only revisit tabs if the group count grows past six with real sub-content per group.

## Persona Red Flags

- **Alex (Impatient Power User)**: Edit drops the Add form's live preview and Docker picker, so "fix the port" is a scroll past fourteen hints, then a redirect that throws his place away — and a keyboard-chosen preset rewrites his subdomain under him.
- **Sam (Accessibility-dependent)**: every hint is unassociated small text, so the entire help layer vanishes for a screen reader; a bare span sits inside a label with no control.
- **Riley (Deliberate Stress Tester)**: Save applies to live proxy traffic with no what-will-change summary; edit-page delete hardwires DNS deletion behind the only guard being the confirm dialog; leaving mid-edit silently discards work.

## Minor Observations

- Pick one vocabulary ("site" on the surface, "backend" for the target) and align h1, nav and hints.
- The form has no action attribute — dead without JS; acceptable given app-wide htmx, but worth a comment.
- The Docker panel is silently omitted on Edit — a port helper lost exactly where ports go stale.
- The enabled checkbox two lines above Save reads like plumbing; consider a status line "This site is served: yes".
- The gate_mode option labels are good ("Yes, with an allowlist" beats acl/open jargon).

## Questions to Consider

1. The apply path reverts the DB and tells you exactly what Caddy rejected — a genuinely safe contract. Why does the UI present save like a mere form persist? Would "Saving applies this at once" as visible copy + inline "Saved and applied" beat the redirect?
2. Months later, do you remember whether "DNS record IP" was deliberate or blank? Should Edit show the record's live state instead of an empty box assuming memory?
3. Who is this form for on the tenth visit — creating or tweaking? That answers regrouping versus more ceremony.
4. Labels are the e2e selectors and hints must stay outside — is aria-describedby (hints stay outside, just id-referenced) worth negotiating, or does the e2e convention outrank the accessibility gain?
