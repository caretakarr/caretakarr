import { test, expect } from '@playwright/test';
import { login } from './helpers';

test('route shows DNS synced; delete asks with the record named', async ({ page }) => {
  await login(page);
  await page.goto('/domains');
  await page.getByLabel('Domain', { exact: true }).fill('dns.example.net');
  await page.getByLabel('Default IP').fill('192.168.1.10');
  await page.getByRole('button', { name: 'Add domain' }).click();
  await expect(page.getByRole('cell', { name: 'dns.example.net' })).toBeVisible();

  await page.goto('/routes/new');
  await page.locator('select[name=domain_id]').selectOption('dns.example.net');
  await page.getByLabel('Preset').selectOption('Radarr');
  await page.getByLabel('Backend host or IP').fill('192.168.1.21');
  await page.getByRole('button', { name: 'Add route' }).click();
  const row = page.getByRole('row', { name: /radarr\.dns\.example\.net/ });
  await expect(row.getByText('synced')).toBeVisible();

  await row.getByRole('button', { name: 'Resync' }).click();
  await expect(row.getByText('synced')).toBeVisible();

  await row.getByRole('link', { name: 'Edit' }).click();
  await expect(page.getByRole('heading', { name: /Edit radarr\.dns\.example\.net/ })).toBeVisible();
  // The shared delete dialog names the record Caretakarr created here.
  await page.getByRole('button', { name: 'Delete route' }).click();
  const dl = page.locator('#delete-dialog');
  await expect(dl.locator('.dlg-host')).toHaveText('radarr.dns.example.net');
  await expect(dl.getByLabel('Also delete the DNS record')).toBeVisible();
  await dl.getByRole('button', { name: 'Delete' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole('cell', { name: 'radarr.dns.example.net' })).toHaveCount(0);
});
