import { test, expect } from '@playwright/test';
import { login } from './helpers';

test('UI host record card shows synced', async ({ page }) => {
  await login(page);
  const card = page.locator('#ui-host');
  await expect(card).toContainText('caretakarr.example.com');
  await expect(card).toContainText('192.168.25.165');
  await expect(card.getByText('synced')).toBeVisible();
});

test('a route named like the UI host is rejected', async ({ page }) => {
  await login(page);
  await page.goto('/domains');
  await page.getByLabel('Domain', { exact: true }).fill('example.com');
  await page.getByLabel('Default IP').fill('192.168.1.10');
  await page.getByRole('button', { name: 'Add domain' }).click();
  await expect(page.getByRole('cell', { name: 'example.com', exact: true })).toBeVisible();

  await page.goto('/routes/new');
  await page.locator('select[name=domain_id]').selectOption('example.com');
  await page.getByLabel('Subdomain').fill('caretakarr');
  await page.getByLabel('Backend host or IP').fill('192.168.1.20');
  await page.getByLabel('Backend port').fill('8080');
  await page.getByRole('button', { name: 'Add route' }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Caretakarr UI host' })).toBeVisible();
});

test('public IPs are rejected for domains', async ({ page }) => {
  await login(page);
  await page.goto('/domains');
  await page.getByLabel('Domain', { exact: true }).fill('public.example.net');
  await page.getByLabel('Default IP').fill('8.8.8.8');
  await page.getByRole('button', { name: 'Add domain' }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'private IPv4' })).toBeVisible();
});
