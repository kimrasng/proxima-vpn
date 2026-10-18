import { test, expect, type Page } from "@playwright/test";

const uniq = () => Math.random().toString(36).slice(2, 9);
const password = "PasskeyE2E-Pass123!";

async function useEnglish(page: Page) {
  await page.addInitScript(() => localStorage.setItem("i18nextLng", "en"));
}

test.describe("passkey authentication", () => {
  test("register, sign in, and remove a passkey", async ({ page, context }) => {
    await useEnglish(page);
    const email = `passkey-${uniq()}@gmail.com`;
    const registered = await page.request.post("/api/v1/auth/register", {
      data: { name: "Passkey E2E", email, password },
    });
    expect(registered.status()).toBe(201);
    const login = await page.request.post("/api/v1/auth/login", { data: { email, password } });
    const { token } = (await login.json()) as { token: string };
    await page.goto("/login");
    await page.evaluate((value) => localStorage.setItem("proxima_user_token", value), token);

    const cdp = await context.newCDPSession(page);
    await cdp.send("WebAuthn.enable");
    await cdp.send("WebAuthn.addVirtualAuthenticator", {
      options: {
        protocol: "ctap2",
        transport: "internal",
        hasResidentKey: true,
        hasUserVerification: true,
        isUserVerified: true,
        automaticPresenceSimulation: true,
      },
    });

    await page.goto("/portal/account");
    await expect(page.getByRole("heading", { name: "Passkeys" })).toBeVisible();
    await page.getByRole("button", { name: "Add a passkey" }).click();
    await page.getByRole("textbox", { name: "Current password", exact: true }).last().fill("wrong-password");
    await page.getByRole("button", { name: "Add a passkey" }).last().click();
    await expect(page.getByText("Current password is incorrect.")).toBeVisible();
    await expect(page.getByText("No passkeys have been added.")).toBeVisible();

    await page.getByRole("textbox", { name: "Current password", exact: true }).last().fill(password);
    await page.getByLabel("Passkey name").fill("E2E test key");
    await page.getByRole("button", { name: "Add a passkey" }).last().click();
    await expect(page.getByText("E2E test key")).toBeVisible({ timeout: 15_000 });

    await page.evaluate(() => localStorage.removeItem("proxima_user_token"));
    await page.goto("/login");
    await page.getByRole("button", { name: "Sign in with a passkey" }).click();
    await expect(page).toHaveURL(/\/portal\/dashboard/, { timeout: 15_000 });

    await page.goto("/portal/account");
    await page.getByRole("button", { name: "Remove", exact: true }).click();
    await expect(page.getByText(/Remove “E2E test key”/)).toBeVisible();
    await page.getByRole("button", { name: "Remove", exact: true }).last().click();
    await expect(page.getByText("No passkeys have been added.")).toBeVisible();

    await page.evaluate(() => localStorage.removeItem("proxima_user_token"));
    await page.goto("/login");
    await page.getByRole("button", { name: "Sign in with a passkey" }).click();
    await expect(page).toHaveURL(/\/login/);
    await expect(page.getByText(/Passkey sign-in|passkey authentication/i)).toBeVisible({ timeout: 15_000 });
    await cdp.detach();
  });

  test("hides the sign-in button when WebAuthn is not supported", async ({ page }) => {
    await page.addInitScript(() => {
      Object.defineProperty(window, "PublicKeyCredential", { configurable: true, value: undefined });
    });
    await page.goto("/login");
    await expect(page.getByRole("button", { name: "Sign in with a passkey" })).toHaveCount(0);
  });
});
