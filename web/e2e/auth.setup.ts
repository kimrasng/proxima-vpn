import { test as setup, expect } from "@playwright/test";

// Logs in once and saves the resulting browser state, so the specs can reuse
// the session instead of signing in per-test. Besides being the Playwright
// convention, this is required here: the API rate-limits login to 10 requests
// per minute per IP (see loginLimiter in api-server/internal/server/routes.go),
// which a per-test login would trip almost immediately and turn into
// confusing "invalid credentials"-looking failures.

// Relative to the package root (where Playwright runs), matching the
// storageState path configured in playwright.config.ts.
export const ADMIN_STATE = "e2e/.auth/admin.json";

const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@example.com";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "Admin1234!";

setup("authenticate as admin", async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.setItem("i18nextLng", "en");
  });

  await page.goto("/admin/login");
  await page.getByRole("textbox", { name: "Email" }).fill(ADMIN_EMAIL);
  await page.getByRole("textbox", { name: "Password" }).fill(ADMIN_PASSWORD);
  await page.getByRole("button", { name: "Sign In" }).click();

  await expect(page).toHaveURL(/\/admin\/dashboard/);

  await page.context().storageState({ path: ADMIN_STATE });
});
