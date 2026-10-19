import { test, expect } from "@playwright/test";

test.describe("signup allowed-domain notice", () => {
  test("shows a compact list of allowed domains when restriction is on", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("i18nextLng", "en"));
    await page.route("**/api/v1/auth/signup-policy", (route) => route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ restricted: true, allowed_domains: ["gmail.com", "qq.com", "163.com"] }),
    }));
    await page.goto("/register");
    const notice = page.locator(".register__domain-notice");
    await expect(notice).toBeVisible();
    await expect(notice.getByText("Allowed email domains")).toBeVisible();
    await expect(notice.locator(".register__domain-chip")).toHaveText(["gmail.com", "qq.com", "163.com"]);
  });

  test("hides the domain notice when restriction is off", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("i18nextLng", "en"));
    await page.route("**/api/v1/auth/signup-policy", (route) => route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ restricted: false, allowed_domains: [] }),
    }));
    await page.goto("/register");
    await expect(page.locator(".register__domain-notice")).toHaveCount(0);
  });
});

test.describe("referral code", () => {
  test("captures ref from login URL, prefills optional field, and submits it", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("i18nextLng", "en"));
    await page.route("**/api/v1/auth/signup-policy", (route) => route.fulfill({
      status: 200, contentType: "application/json",
      body: JSON.stringify({ restricted: false, allowed_domains: [] }),
    }));
    await page.route("**/api/v1/auth/register/request-code", async (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ message: "sent", expires_in_seconds: 600 }) }));
    await page.route("**/api/v1/auth/register/verify-code", async (route) => {
      expect(route.request().postDataJSON()).toMatchObject({ referral_code: "FRIEND-42", code: "123456" });
      await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ id: "id", email: "new@example.com", name: "New", sub_token: "token" }) });
    });

    await page.goto("/login?ref=FRIEND-42");
    await page.getByRole("link", { name: "Don't have an account? Register" }).click();
    const referral = page.getByRole("textbox", { name: "Referral code (optional)" });
    await expect(referral).toHaveValue("FRIEND-42");
    await page.getByRole("textbox", { name: "Name" }).fill("New");
    await page.getByRole("textbox", { name: "Email" }).fill("new@example.com");
    await page.getByRole("textbox", { name: "Password", exact: true }).fill("Passw0rd!test42");
    await page.getByRole("textbox", { name: "Confirm Password" }).fill("Passw0rd!test42");
    await page.getByRole("button", { name: "Create Account" }).click();
    await page.getByRole("textbox", { name: "6-digit verification code" }).fill("123456");
    await page.getByRole("button", { name: "Verify and create account" }).click();
    await expect(page.getByText(/Registration successful/i)).toBeVisible();
  });

  test("captures ref from the root URL and restores it on a later registration visit", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("i18nextLng", "en"));
    await page.route("**/api/v1/auth/signup-policy", (route) => route.fulfill({
      status: 200, contentType: "application/json",
      body: JSON.stringify({ restricted: false, allowed_domains: [] }),
    }));
    await page.goto("/?ref=ROOT-9");
    await expect(page).toHaveURL(/\/login\?ref=ROOT-9/);
    await page.goto("/register");
    await expect(page.getByRole("textbox", { name: "Referral code (optional)" })).toHaveValue("ROOT-9");
  });
});
