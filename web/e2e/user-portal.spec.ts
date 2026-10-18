import { test, expect, type Page } from "@playwright/test";

// Browser e2e for the end-user portal: register -> log in -> see the portal.
// Device creation is asserted separately because it depends on the account
// having a plan, which only an admin can assign.

async function useEnglish(page: Page) {
  await page.addInitScript(() => {
    window.localStorage.setItem("i18nextLng", "en");
  });
}

const uniq = () => Math.random().toString(36).slice(2, 8);

test.describe("user authentication", () => {
  test("unauthenticated visit to the portal redirects to login", async ({ page }) => {
    await useEnglish(page);
    await page.goto("/portal/devices");
    await expect(page).toHaveURL(/\/login/);
  });

  test("register, then sign in with the new account", async ({ page }) => {
    await useEnglish(page);

    const email = `e2e-ui-${uniq()}@example.com`;
    const password = "E2EUiPass123!";

    await page.route("**/api/v1/auth/register/request-code", async (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ message: "sent", expires_in_seconds: 600 }) }));
    await page.route("**/api/v1/auth/register/verify-code", async (route) => route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ id: "test-user", email, name: "E2E UI User", sub_token: "test-sub-token" }) }));
    await page.route("**/api/v1/auth/login", async (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ token: "test-user-token" }) }));
    await page.route("**/api/v1/auth/register", async (route) => {
      const body = route.request().postDataJSON() as { code?: string };
      if (!body.code) return route.fulfill({ status: 400, body: "verification code required" });
      await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ id: "test-user", email, name: "E2E UI User", sub_token: "test-sub-token" }) });
    });

    await page.goto("/register");
    await page.getByRole("textbox", { name: "Name" }).fill("E2E UI User");
    await page.getByRole("textbox", { name: "Email" }).fill(email);
    await page.getByRole("textbox", { name: "Password", exact: true }).fill(password);
    await page.getByRole("textbox", { name: "Confirm Password" }).fill(password);
    await page.getByRole("button", { name: "Create Account" }).click();

    await expect(page.getByRole("textbox", { name: "6-digit verification code" })).toBeVisible();
    await page.getByRole("textbox", { name: "6-digit verification code" }).fill("123456");
    await page.getByRole("button", { name: "Verify and create account" }).click();
    await expect(page.getByText(/Registration successful/i)).toBeVisible();

    // The app redirects to login after a successful registration.
    await expect(page).toHaveURL(/\/login/, { timeout: 20_000 });

    await page.getByRole("textbox", { name: "Email" }).fill(email);
    await page.getByRole("textbox", { name: "Password" }).fill(password);
    await page.getByRole("button", { name: "Sign In", exact: true }).click();

    await expect(page).toHaveURL(/\/portal\//);
  });

  test("password mismatch is caught client-side", async ({ page }) => {
    await useEnglish(page);
    await page.goto("/register");

    await page.getByRole("textbox", { name: "Name" }).fill("Mismatch User");
    await page.getByRole("textbox", { name: "Email" }).fill(`e2e-mismatch-${uniq()}@example.com`);
    await page.getByRole("textbox", { name: "Password", exact: true }).fill("Password123!");
    await page.getByRole("textbox", { name: "Confirm Password" }).fill("Different123!");
    await page.getByRole("button", { name: "Create Account" }).click();
    await expect(page.getByText(/Passwords do not match/i)).toBeVisible();
    await expect(page).toHaveURL(/\/register/);
  });

  test("wrong credentials are rejected", async ({ page }) => {
    await useEnglish(page);
    await page.goto("/login");

    await page.getByRole("textbox", { name: "Email" }).fill("nobody-e2e@example.com");
    await page.getByRole("textbox", { name: "Password" }).fill("wrong-password");
    await page.getByRole("button", { name: "Sign In", exact: true }).click();

    // Login.tsx surfaces the API's own error text when present, falling back
    // to the translated message.
    await expect(
      page.getByText(/invalid credentials|Login failed/i).first(),
    ).toBeVisible();
    await expect(page).toHaveURL(/\/login/);
  });
});

test.describe("user portal pages", () => {
  test("a freshly registered user can browse every portal page", async ({ page }) => {
    await useEnglish(page);

    const email = `e2e-ui-nav-${uniq()}@example.com`;
    const password = "E2EUiPass123!";

    await page.route("**/api/v1/auth/register/request-code", async (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ message: "sent", expires_in_seconds: 600 }) }));
    await page.route("**/api/v1/auth/register/verify-code", async (route) => route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ id: "test-user", email, name: "E2E Nav User", sub_token: "test-sub-token" }) }));
    await page.route("**/api/v1/auth/login", async (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ token: "test-user-token" }) }));
    await page.route("**/api/v1/auth/register", async (route) => route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ id: "test-user", email, name: "E2E Nav User", sub_token: "test-sub-token" }) }));

    await page.goto("/register");
    await page.getByRole("textbox", { name: "Name" }).fill("E2E Nav User");
    await page.getByRole("textbox", { name: "Email" }).fill(email);
    await page.getByRole("textbox", { name: "Password", exact: true }).fill(password);
    await page.getByRole("textbox", { name: "Confirm Password" }).fill(password);
    await page.getByRole("button", { name: "Create Account" }).click();
    await expect(page.getByRole("textbox", { name: "6-digit verification code" })).toBeVisible();
    await page.getByRole("textbox", { name: "6-digit verification code" }).fill("123456");
    await page.getByRole("button", { name: "Verify and create account" }).click();
    await expect(page).toHaveURL(/\/login/, { timeout: 20_000 });

    await page.getByRole("textbox", { name: "Email" }).fill(email);
    await page.getByRole("textbox", { name: "Password" }).fill(password);
    await page.getByRole("button", { name: "Sign In", exact: true }).click();
    await expect(page).toHaveURL(/\/portal\//);

    // Each portal page fetches its own data on mount; a 4xx/5xx or a render
    // crash surfaces as a missing heading here.
    for (const path of [
      "/portal/devices",
      "/portal/traffic",
      "/portal/plan",
      "/portal/announcements",
      "/portal/account",
    ]) {
      await page.goto(path);
      await expect(page.locator("h1, h2").first()).toBeVisible();
      await expect(page.getByText(/Failed to load/i)).toHaveCount(0);
    }
  });
});
