import { test, expect } from '@playwright/test';
import { login } from './helpers';

test('add a route from a preset, edit it, delete it', async ({ page }) => {
  await login(page);
  await page.goto('/domains');
  await page.getByLabel('Domain', { exact: true }).fill('routes.example.net');
  await page.getByLabel('Default IP').fill('192.168.1.10');
  await page.getByRole('button', { name: 'Add domain' }).click();
  await expect(page.getByRole('cell', { name: 'routes.example.net' })).toBeVisible();

  await page.goto('/routes/new');
  await page.locator('select[name=domain_id]').selectOption('routes.example.net');
  await page.getByLabel('Preset').selectOption('Sonarr');
  await expect(page.getByLabel('Subdomain')).toHaveValue('sonarr');
  await expect(page.getByLabel('Backend port')).toHaveValue('8989');
  await page.getByLabel('Backend host or IP').fill('192.168.1.20');
  await page.getByRole('button', { name: 'Add route' }).click();
  await expect(page).toHaveURL(/\/$/);
  const row = page.getByRole('row', { name: /sonarr\.routes\.example\.net/ });
  await expect(row.getByRole('cell', { name: '192.168.1.20:8989' })).toBeVisible();

  await row.getByRole('link', { name: 'Edit' }).click();
  await expect(page.getByRole('heading', { name: /Edit sonarr\.routes\.example\.net/ })).toBeVisible();
  await page.getByLabel('Enabled').uncheck();
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText('Saved and applied')).toBeVisible();
  await expect(page.getByRole('heading', { name: /Edit sonarr/ })).toBeVisible();

  await page.goto('/');
  const drow = page.getByRole('row', { name: /sonarr\.routes\.example\.net/ });
  await expect(drow.getByRole('cell', { name: 'disabled' })).toBeVisible();

  await drow.getByRole('link', { name: 'Edit' }).click();
  await expect(page.getByRole('heading', { name: /Edit sonarr/ })).toBeVisible();
  await page.getByRole('button', { name: 'Delete route' }).click();
  await page.locator('#delete-dialog').getByRole('button', { name: 'Delete' }).click();
  await expect(page.getByRole('cell', { name: 'sonarr.routes.example.net' })).toHaveCount(0);
});
