import { test, expect } from "@playwright/test";

test.describe("email verification during sign-up", () => {
  test("collects the six-digit code before creating the account", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("i18nextLng", "en"));
    const calls: string[] = [];
    await page.route("**/api/v1/auth/signup-policy", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ restricted: false, allowed_domains: [] }) }));
    await page.route("**/api/v1/auth/register/request-code", async (route) => {
      calls.push("request");
      expect(route.request().postDataJSON()).toMatchObject({ language: "en" });
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ message: "verification code sent", expires_in_seconds: 600 }) });
    });
    await page.route("**/api/v1/auth/register/verify-code", async (route) => {
      calls.push("verify");
      await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ id: "id", email: "verify@example.com", name: "Verify", sub_token: "token" }) });
    });
    await page.route("**/api/v1/auth/register", async (route) => {
      calls.push("direct-register");
      const body = route.request().postDataJSON() as { code?: string };
      if (!body.code) return route.fulfill({ status: 400, body: "verification code required" });
      await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ id: "id", email: "verify@example.com", name: "Verify", sub_token: "token" }) });
    });
    await page.goto("/register");
    await page.getByRole("textbox", { name: "Name" }).fill("Verify");
    await page.getByRole("textbox", { name: "Email" }).fill("verify@example.com");
    await page.getByRole("textbox", { name: "Password", exact: true }).fill("Passw0rd!Verify42");
    await page.getByRole("textbox", { name: "Confirm Password" }).fill("Passw0rd!Verify42");
    await page.getByRole("button", { name: "Create Account" }).click();
    await expect(page.getByRole("textbox", { name: "6-digit verification code" })).toBeVisible();
    expect(calls).toEqual(["request"]);
    const resending = page.getByRole("button", { name: "Resend code" });
    expect(resending).toBeTruthy();
    await resending.click();
    await expect(page.getByRole("textbox", { name: "6-digit verification code" })).toHaveValue("");
    await expect(page.locator("form").getByText("We sent a 6-digit code to verify@example.com.")).toBeVisible();
    await page.getByRole("textbox", { name: "6-digit verification code" }).fill("123456");
    await expect(page.getByRole("textbox", { name: "6-digit verification code" })).toHaveValue("123456");
    await page.getByRole("button", { name: "Verify and create account" }).click();
    await expect(page.getByText(/Registration successful/i)).toBeVisible();
    expect(calls).toEqual(["request", "request", "verify"]);
  });
});
