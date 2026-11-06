import { defineConfig, devices } from "@playwright/test";

// HTTP fixtures exercise the production UI, not the backend or suppliers.
// The caller starts one owned preview of the current production build.
export default defineConfig({
  testDir: "./e2e",
  testMatch: /connection-history\.admin\.spec\.ts/,
  outputDir: "test-results/history",
  workers: 1,
  retries: 0,
  timeout: 45_000,
  expect: { timeout: 10_000 },
  reporter: [["list"]],
  use: {
    baseURL: "http://127.0.0.1:4183",
    channel: "chrome",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  projects: [
    { name: "desktop-light", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 1000 }, colorScheme: "light" } },
    { name: "desktop-dark", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 1000 }, colorScheme: "dark" } },
    { name: "mobile-light", use: { ...devices["iPhone 14"], defaultBrowserType: "chromium", colorScheme: "light" } },
    { name: "mobile-dark", use: { ...devices["iPhone 14"], defaultBrowserType: "chromium", colorScheme: "dark" } },
  ],
});
