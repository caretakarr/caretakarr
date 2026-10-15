import { createServer } from 'http';
import { test, expect } from '@playwright/test';
import { login } from './helpers';

// The gate is enforced by Caddy on the route's own host: HTTPS via the internal issuer
// (playwright.config.ts sets ACME_ISSUER=internal) and Chromium's host-resolver-rules maps the
// domain to 127.0.0.1. ignoreHTTPSErrors trusts Caddy's self-signed cert.
const GATE = 'https://st.gate.example.test:28443';

test.use({ ignoreHTTPSErrors: true });

test('gated route: magic link in, revoke out', async ({ page }) => {
  const upstream = createServer((req, res) => res.end('GATE-UPSTREAM'));
  await new Promise<void>((ok) => upstream.listen(29111, '127.0.0.1', ok));

  await login(page);

  // Email delivery is configured in the UI now: point it at the SMTP sink before gating a site.
  await page.goto('/settings');
  await page.getByLabel('SMTP host', { exact: true }).fill('127.0.0.1');
  await page.getByLabel('SMTP port').fill('28097');
  await page.getByLabel('MAIL FROM', { exact: true }).fill('e2e@example.com');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText('On. Magic-link sites send their links from here.')).toBeVisible();
  await page.goto('/domains');
  await page.getByLabel('Domain', { exact: true }).fill('gate.example.test');
  await page.getByLabel('Default IP').fill('192.168.1.19');
  await page.getByRole('button', { name: 'Add domain' }).click();
  await expect(page.getByRole('cell', { name: 'gate.example.test' })).toBeVisible();

  await page.goto('/routes/new');
  await page.locator('select[name=domain_id]').selectOption('gate.example.test');
  await page.getByLabel('Subdomain').fill('st');
  await page.getByLabel('Backend host or IP').fill('127.0.0.1');
  await page.getByLabel('Backend port').fill('29111');
  await page.locator('select[name=gate_mode]').selectOption('open');
  await page.getByRole('button', { name: 'Add route' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole('row', { name: /st\.gate\.example\.test/ }).getByText('gated (any email)')).toBeVisible();

  // The route form's magic-link section is visible unconfigured, too — but disabled there.
  await page.goto('/domains');
  await page.getByLabel('Domain', { exact: true }).fill('gate2.example.test');
  await page.getByLabel('Default IP').fill('192.168.1.18');
  await page.getByRole('button', { name: 'Add domain' }).click();
  await expect(page.getByRole('cell', { name: 'gate2.example.test' })).toBeVisible();
  await page.goto('/sites/new');
  await expect(page.getByRole('button', { name: 'Add site' })).toBeVisible();
  await expect(page.locator('fieldset', { has: page.getByText('Magic-link access (optional)') })).toBeVisible();

  // Unauthenticated: the portal answers on the route host, the app never sees the request.
  await page.goto(GATE);
  await expect(page.getByRole('heading', { name: 'Request access to st.gate.example.test' })).toBeVisible();

  // Request a link; the page is the same whatever the address (enumeration-neutral).
  await page.getByPlaceholder('you@example.com').fill('visitor@example.net');
  await page.getByRole('button', { name: 'Send me a link' }).click();
  await expect(page.getByRole('heading', { name: 'Check your email' })).toBeVisible();

  const mail = await (await fetch('http://127.0.0.1:28098/last')).text();
  const link = mail.match(/https:\/\/\S+\/_gate\/verify\/\S+/)?.[0];
  expect(link, 'the SMTP sink captured the magic link').toBeTruthy();

  // Clicking the link grants the session on this host and lands on the site.
  await page.goto(link);
  await expect(page).toHaveURL(`${GATE}/`);
  await expect(page.getByText('GATE-UPSTREAM')).toBeVisible();
  await expect(page.getByText('Send me a link')).toBeHidden();

  // The operator sees the identity and revokes it from Security; the site gates again.
  await page.goto('http://127.0.0.1:28081/security');
  const row = page.getByRole('row', { name: /visitor@example\.net/ });
  await expect(row).toBeVisible();
  page.once('dialog', (d) => d.accept());
  await row.getByRole('button', { name: 'Revoke' }).click();
  await expect(page.getByRole('cell', { name: 'visitor@example.net' })).toBeHidden();

  await page.goto(GATE);
  await expect(page.getByRole('heading', { name: 'Request access' })).toBeVisible();

  // Shared server: remove the route so later specs see the same checklist state they expect.
  await page.goto('http://127.0.0.1:28081/');
  await expect(page.getByRole('row', { name: /st\.gate\.example\.test/ }).getByRole('link', { name: 'Edit' })).toBeVisible();
  await page.getByRole('row', { name: /st\.gate\.example\.test/ }).getByRole('button', { name: 'Delete' }).click();
  await page.locator('#delete-dialog').getByRole('button', { name: 'Delete' }).click();
  await expect(page.getByRole('cell', { name: 'st.gate.example.test' })).toBeHidden();

  await new Promise<void>((ok) => upstream.close(() => ok()));
});