import { test, expect } from "@playwright/test";

test.describe("signup allowed-domain info popup", () => {
  test("shows the full allow-list in a popup when the restriction is on", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("i18nextLng", "en"));
    await page.route("**/api/v1/auth/signup-policy", (route) => route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ restricted: true, allowed_domains: ["gmail.com", "qq.com", "163.com"] }),
    }));
    await page.goto("/register");
    const info = page.getByRole("button", { name: "Show allowed email domains" });
    await expect(info).toBeVisible();
    await expect(page.getByTestId("allowed-domains")).toContainText("gmail.com");
    await info.click();
    const dialog = page.getByRole("dialog", { name: "Allowed email domains" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByTestId("allowed-domains-modal")).toHaveText("gmail.com  ·  qq.com  ·  163.com");
    await dialog.getByRole("button", { name: "Close" }).click();
    await expect(dialog).toHaveCount(0);
  });

  test("hides the info popup button when the restriction is off", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("i18nextLng", "en"));
    await page.route("**/api/v1/auth/signup-policy", (route) => route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ restricted: false, allowed_domains: [] }),
    }));
    await page.goto("/register");
    await expect(page.getByRole("button", { name: "Show allowed email domains" })).toHaveCount(0);
  });
});
