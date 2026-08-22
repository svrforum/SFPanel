import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  reporter: 'list',
  timeout: 30000,
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3628',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    // A panel with server.tls.enabled serves a certificate from its own local
    // CA, which Chromium has no reason to trust. Without this every request
    // fails at the handshake and the whole suite reports as a connection
    // error rather than as anything about the app. Safe here: the tests only
    // ever target a panel the runner started or was pointed at explicitly.
    ignoreHTTPSErrors: true,
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium' },
    },
  ],
})
