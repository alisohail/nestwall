import { defineConfig, devices } from '@playwright/test';
import { WEB_PORT } from './e2e/support/stack';

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/*.spec.ts',
  outputDir: './e2e/test-output',
  fullyParallel: false,
  workers: 1,
  // The journey is stateful and runs once on a fresh stack. A retry would see
  // leftover data, so failures are reported on the first attempt.
  retries: 0,
  forbidOnly: !!process.env['CI'],
  reporter: [['list']],
  timeout: 90_000,
  globalSetup: './e2e/global-setup.ts',
  use: {
    baseURL: `http://localhost:${WEB_PORT}`,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      // Every journey except restore. They share one stack, so order does not matter.
      name: 'journeys',
      testIgnore: '**/backup-restore.spec.ts',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      // Restore stops the API process, so it runs after the other journeys have finished.
      name: 'restore',
      testMatch: '**/backup-restore.spec.ts',
      dependencies: ['journeys'],
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: `node node_modules/@angular/cli/bin/ng.js serve --port ${WEB_PORT} --proxy-config e2e/proxy.conf.json`,
    url: `http://localhost:${WEB_PORT}`,
    // Reusing a server is opt-in: a stale dev server on this port could proxy to the wrong API.
    reuseExistingServer: process.env['E2E_REUSE_WEB'] === '1',
    stdout: 'ignore',
    stderr: 'pipe',
    timeout: 240_000,
  },
});
