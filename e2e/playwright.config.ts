import { defineConfig } from '@playwright/test';

// Runs the built binary directly: UI on loopback, Caddy on high ports (ACME fails offline; the UI does not care).
// Cloudflare is mocked by cf-mock.ts.
const UI = '127.0.0.1:28081';

export default defineConfig({
  testDir: './tests',
  workers: 1,
  use: {
    baseURL: `http://${UI}`,
    // Env proxies (HTTPS_PROXY/HTTP_PROXY in CI runners and sandboxes) are picked up by
    // Playwright and handed to Chromium per browser context via CDP — launch flags like
    // --no-proxy-server and the context option proxy: { server: 'direct://' } do NOT
    // override them there (both verified failing). A context proxy with bypass '**'
    // replaces the env proxy: every host bypasses and the browser connects directly.
    proxy: { server: 'http://127.0.0.1:9', bypass: '**' },
    launchOptions: { args: ['--host-resolver-rules=MAP *.gate.example.test 127.0.0.1'] },
  },
  webServer: [
    {
      command: 'bun cf-mock.ts',
      url: 'http://127.0.0.1:28099/user/tokens/verify',
      reuseExistingServer: false,
    },
    {
      command: 'bun smtp-mock.ts',
      url: 'http://127.0.0.1:28098/ping',
      reuseExistingServer: false,
    },
    {
      command: 'rm -rf data && ../bin/caretakarr',
      url: `http://${UI}/login`,
      reuseExistingServer: false,
      env: {
        CLOUDFLARE_API_TOKEN: 'e2e0000000000000000000000000000000000000',
        CLOUDFLARE_BASE_URL: 'http://127.0.0.1:28099',
        CARETAKARR_HOST: 'caretakarr.example.com',
        CARETAKARR_HOST_IP: '192.168.25.165',
        ACME_EMAIL: 'e2e@example.com',
        ACME_STAGING: 'true',
        ADMIN_PASSWORD: 'e2e-pass',
        CARETAKARR_SECRET_KEY: 'e2e-secret-key-0123456789abcdef-e2e',
        UI_LISTEN: UI,
        HTTP_PORT: '28080',
        HTTPS_PORT: '28443',
        DATA_DIR: './data',
        ACME_ISSUER: 'internal',
      },
    },
  ],
});
