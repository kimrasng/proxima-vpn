import { defineConfig, devices } from "@playwright/test";

// Synthetic data only: these tests never mutate a live admin deployment.
export default defineConfig({
  testDir: "./e2e",
  testMatch: /(?:route-management|route-filters|plan-routes|device-bandwidth|uuid-evictions|managed-node-chains|node-endpoints)\.admin\.spec\.ts/,
  workers: 1,
  timeout: 45_000,
  expect: { timeout: 10_000 },
  reporter: [["list"]],
  use: { ...devices["Desktop Chrome"], baseURL: "http://127.0.0.1:4182", screenshot: "only-on-failure", trace: "retain-on-failure" },
  webServer: { command: "npm run dev -- --port 4182 --host 127.0.0.1", url: "http://127.0.0.1:4182", reuseExistingServer: false },
});
