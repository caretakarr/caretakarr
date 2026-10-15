import { test, expect } from '@playwright/test';
import { login } from './helpers';

test('toggle a route and edit allowlist and basic auth', async ({ page }) => {
  await login(page);
  await page.goto('/domains');
  await page.getByLabel('Domain', { exact: true }).fill('controls.example.net');
  await page.getByLabel('Default IP').fill('192.168.1.10');
  await page.getByRole('button', { name: 'Add domain' }).click();
  await expect(page.getByRole('cell', { name: 'controls.example.net' })).toBeVisible();

  await page.goto('/routes/new');
  await page.locator('select[name=domain_id]').selectOption('controls.example.net');
  await page.getByLabel('Preset').selectOption('Bazarr');
  await page.getByLabel('Backend host or IP').fill('192.168.1.22');
  await page.getByLabel('Allowed sources').fill('192.168.0.0/16');
  await page.getByLabel('User', { exact: true }).fill('alice');
  await page.getByLabel('Password').fill('s3cret');
  await page.getByRole('button', { name: 'Add route' }).click();
  const row = page.getByRole('row', { name: /bazarr\.controls\.example\.net/ });
  await expect(row).toBeVisible();
  await expect(row.getByRole('cell', { name: 'enabled' })).toBeVisible();

  await row.getByRole('button', { name: 'Disable' }).click();
  await expect(row.getByRole('cell', { name: 'disabled' })).toBeVisible();
  await row.getByRole('button', { name: 'Enable' }).click();
  await expect(row.getByRole('cell', { name: 'enabled' })).toBeVisible();

  await row.getByRole('link', { name: 'Edit' }).click();
  await expect(page.getByLabel('Allowed sources')).toHaveValue('192.168.0.0/16');
  await expect(page.getByLabel('User', { exact: true })).toHaveValue('alice');
  await expect(page.getByLabel('Password')).toHaveValue('');
  await page.getByLabel('Allowed sources').fill('not-a-cidr');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'not-a-cidr' })).toBeVisible();
});
