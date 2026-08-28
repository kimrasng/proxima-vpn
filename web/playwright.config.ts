import { defineConfig, devices } from "@playwright/test";

// Browser-level e2e for the admin panel and user portal. These drive the
// real built frontend against a real api-server (the same one the Go/CLI e2e
// suite uses), so they cover the layer nothing else did: that the pages
// actually render, that forms submit what the API expects, and that the
// responses land back in the UI. `npm run build` passing says nothing about
// any of that - the "Settings save always 500s" bug lived in exactly this gap.
//
// BASE_URL points at the preview server (vite preview proxies /api and /sub
// to the api-server, mirroring nginx in production - see vite.config.ts).
const baseURL = process.env.E2E_BASE_URL || "http://localhost:8080";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false, // these share one backend + one admin account
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : [["list"]],
  timeout: 60_000,
  expect: { timeout: 15_000 },

  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },

  projects: [
    // Signs in once and stores the session; see e2e/auth.setup.ts for why
    // (the API rate-limits logins, so per-test sign-in is not viable).
    { name: "setup", testMatch: /.*\.setup\.ts/ },

    // Specs that drive the panel as a logged-in admin.
    {
      name: "admin",
      testMatch: /admin\.spec\.ts/,
      dependencies: ["setup"],
      use: { ...devices["Desktop Chrome"], storageState: "e2e/.auth/admin.json" },
    },

    // The portal specs cover sign-up/sign-in themselves, so they must start
    // from a clean, unauthenticated context.
    {
      name: "user-portal",
      testMatch: /user-portal\.spec\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
  ],

  // In CI the workflow starts the preview server itself (so it can also
  // control the api-server lifecycle); locally, start it on demand.
  webServer: process.env.E2E_NO_WEBSERVER
    ? undefined
    : {
        command: "npm run preview",
        url: baseURL,
        reuseExistingServer: !process.env.CI,
        timeout: 60_000,
      },
});
