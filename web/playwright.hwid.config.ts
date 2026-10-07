import { defineConfig, devices } from "@playwright/test";

// Mock-only browser checks, independent of a live account or database.
export default defineConfig({
  testDir: "./e2e",
  testMatch: /subscription-account\.user\.spec\.ts/,
  workers: 1,
  timeout: 45_000,
  expect: { timeout: 10_000 },
  use: { ...devices["Desktop Chrome"], baseURL: "http://127.0.0.1:4183", screenshot: "only-on-failure", trace: "retain-on-failure" },
  webServer: { command: "npm run dev -- --port 4183 --host 127.0.0.1", url: "http://127.0.0.1:4183", reuseExistingServer: false },
});
