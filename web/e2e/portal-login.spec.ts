import { test, expect, type Page } from "@playwright/test";

// Browser e2e for the portal sign-in screen and the password-reset pages.
// These only cover what the browser can decide on its own (layout, carousel,
// language switch, client-side validation, the reset-link states). The real
// email round trip needs an SMTP server and is verified separately.

async function useLanguage(page: Page, lng: "ko" | "en" | "zh") {
  await page.addInitScript((l) => {
    window.localStorage.setItem("i18nextLng", l);
  }, lng);
}

test.describe("portal sign-in screen", () => {
  test("shows the portal brand, a forgot-password link and no legal footer", async ({ page }) => {
    await useLanguage(page, "en");
    await page.goto("/login");

    await expect(page.getByText("Proxima Portal")).toBeVisible();
    await expect(page.getByText("Your connection. Your freedom.")).toBeVisible();
    await expect(page.getByRole("link", { name: "Forgot password?" })).toBeVisible();
    await expect(page.getByRole("link", { name: /Don't have an account\? Register/ })).toBeVisible();
    await expect(page.getByText(/terms of service|privacy policy|이용약관|개인정보처리방침/i)).toHaveCount(0);
  });

  test("highlights swipe track: dots, keyboard, wrap-around and pause", async ({ page }) => {
    await useLanguage(page, "en");
    await page.goto("/login");

    const dots = page.locator(".portal-highlights__dot");
    const active = () => dots.evaluateAll((els) => els.findIndex((el) => el.className.includes("--active")));
    await expect(dots).toHaveCount(4);
    expect(await active()).toBe(0);

    await dots.nth(2).click();
    await expect.poll(active).toBe(2);

    // A touch swipe is a native scroll of the snap track.
    await page
      .locator(".portal-highlights__track")
      .evaluate((el) => el.scrollTo({ left: el.clientWidth * 3, behavior: "instant" }));
    await expect.poll(active).toBe(3);

    await page.locator(".portal-highlights__track").focus();
    await page.keyboard.press("ArrowRight");
    await expect.poll(active).toBe(0);

    // Once paused, it must not advance on its own.
    await page.getByRole("button", { name: "Pause automatic rotation" }).click();
    await page.mouse.move(2, 2);
    await page.waitForTimeout(5500);
    expect(await active()).toBe(0);
    await expect(page.getByRole("button", { name: "Resume automatic rotation" })).toBeVisible();
  });

  test("language dropdown switches the whole screen", async ({ page }) => {
    await useLanguage(page, "ko");
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "로그인" })).toBeVisible();

    await page.getByRole("button", { name: /언어 선택/ }).click();
    await page.getByRole("menuitem", { name: "English" }).click();

    await expect(page.getByRole("heading", { name: "Login" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Forgot password?" })).toBeVisible();
  });

  test("does not overflow horizontally on a phone", async ({ page }) => {
    await useLanguage(page, "ko");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "로그인" })).toBeVisible();
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    );
    expect(overflow).toBe(false);
  });

  test("language button stays on one line and the title is a sane size", async ({ page }) => {
    await useLanguage(page, "ko");
    await page.goto("/login");
    const button = page.getByRole("button", { name: /언어 선택/ });
    await expect(button).toBeVisible();

    // Globe, label and caret share one row: their vertical centres line up and
    // the button is a normal single-line height.
    const geometry = await button.evaluate((el) => {
      const centre = (node: Element) => {
        const b = node.getBoundingClientRect();
        return b.top + b.height / 2;
      };
      const label = el.querySelector(".auth-layout__lang-label span");
      const icons = [...el.querySelectorAll("svg")];
      return {
        height: el.getBoundingClientRect().height,
        text: label ? centre(label) : NaN,
        icons: icons.map(centre),
      };
    });
    expect(geometry.height).toBeLessThan(48);
    expect(geometry.icons.length).toBeGreaterThanOrEqual(2);
    for (const centre of geometry.icons) expect(Math.abs(centre - geometry.text)).toBeLessThan(6);

    await expect(page.getByText("Proxima Portal")).toHaveCSS("font-size", "24px");
  });

  test("the password field keeps a clean accessible name and the link sits on its label row", async ({ page }) => {
    await useLanguage(page, "en");
    await page.goto("/login");
    const input = page.getByRole("textbox", { name: "Password", exact: true });
    await expect(input).toBeVisible();

    const link = await page.getByRole("link", { name: "Forgot password?" }).boundingBox();
    const field = await input.boundingBox();
    const label = await page.locator("main form label", { hasText: "Password" }).boundingBox();
    expect(link && field && label).toBeTruthy();
    expect(Math.abs(link!.y + link!.height / 2 - (label!.y + label!.height / 2))).toBeLessThan(10);
    expect(Math.abs(link!.x + link!.width - (field!.x + field!.width))).toBeLessThanOrEqual(2);
  });

  test("nothing jumps when slides rotate or validation errors appear", async ({ page }) => {
    await useLanguage(page, "ko");
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "로그인" })).toBeVisible();

    const title = page.getByText("Proxima Portal");
    const form = page.locator("main form");
    const carousel = page.locator(".portal-highlights");
    // Page-relative, so scrolling the viewport (small screens) is not mistaken for a layout jump.
    const top = async (locator: typeof title) =>
      locator.evaluate((el) => Math.round(el.getBoundingClientRect().top + window.scrollY));

    const titleY = await top(title);
    const formY = await top(form);
    const carouselHeight = Math.round((await carousel.boundingBox())!.height);

    for (let i = 0; i < 4; i++) {
      await page.locator(".portal-highlights__dot").nth(i).click();
      await page.waitForTimeout(700);
      expect(await top(title)).toBe(titleY);
      expect(await top(form)).toBe(formY);
      expect(Math.round((await carousel.boundingBox())!.height)).toBe(carouselHeight);
    }

    // Submitting empty fields adds error text under the inputs; the card grows
    // downwards but nothing above it may move.
    await page.locator("main form").getByRole("button", { name: "로그인" }).click();
    await expect(page.getByText("필수 항목을 입력하세요.").first()).toBeVisible();
    expect(await top(title)).toBe(titleY);
    expect(await top(form)).toBe(formY);
  });
});

test.describe("password reset pages", () => {
  test("the sign-in link leads to the forgot-password form and validates the email", async ({ page }) => {
    await useLanguage(page, "en");
    await page.goto("/login");
    await page.getByRole("link", { name: "Forgot password?" }).click();
    await expect(page).toHaveURL(/\/forgot-password/);
    await expect(page.getByRole("heading", { name: "Forgot password" })).toBeVisible();

    await page.getByRole("button", { name: "Send reset link" }).click();
    await expect(page.getByText("This field is required.")).toBeVisible();

    await page.getByRole("textbox", { name: "Email" }).fill("not-an-email");
    await page.getByRole("button", { name: "Send reset link" }).click();
    await expect(page.getByText("Enter a valid email address.").first()).toBeVisible();
  });

  test("the forgot-password form never reveals whether an account exists", async ({ page }) => {
    await useLanguage(page, "en");
    // Whatever the address, a configured server answers 200 with the same body.
    await page.route("**/api/v1/auth/forgot-password", (route) =>
      route.fulfill({ status: 200, json: { message: "ok" } }),
    );
    await page.goto("/forgot-password");
    await page.getByRole("textbox", { name: "Email" }).fill("nobody-e2e@example.com");
    await page.getByRole("button", { name: "Send reset link" }).click();

    await expect(page.getByRole("heading", { name: "Check your email" })).toBeVisible();
    await expect(page.getByText(/If an account exists for that address/)).toBeVisible();
  });

  test("an unconfigured mailer is explained instead of failing silently", async ({ page }) => {
    await useLanguage(page, "en");
    await page.route("**/api/v1/auth/forgot-password", (route) =>
      route.fulfill({ status: 503, json: { error: "password reset email is not configured" } }),
    );
    await page.goto("/forgot-password");
    await page.getByRole("textbox", { name: "Email" }).fill("someone@example.com");
    await page.getByRole("button", { name: "Send reset link" }).click();
    await expect(page.getByText(/not available right now/)).toBeVisible();
  });

  test("a reset page without a token shows the invalid-link state", async ({ page }) => {
    await useLanguage(page, "en");
    await page.goto("/reset-password");
    await expect(page.getByRole("heading", { name: "This link cannot be used" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Get a new link" })).toBeVisible();
    await page.getByRole("button", { name: "Get a new link" }).click();
    await expect(page).toHaveURL(/\/forgot-password/);
  });

  test("the token is removed from the address bar and mismatched passwords are caught", async ({ page }) => {
    await useLanguage(page, "en");
    await page.goto("/reset-password?token=abc123");
    await expect(page.getByRole("heading", { name: "Set a new password" })).toBeVisible();
    expect(page.url()).not.toContain("token=");

    await page.getByRole("textbox", { name: "New password", exact: true }).fill("Passw0rd!x");
    await page.getByRole("textbox", { name: "Confirm new password" }).fill("different");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByText("Passwords do not match.")).toBeVisible();
  });

  test("an expired or used token shows the invalid-link state", async ({ page }) => {
    await useLanguage(page, "en");
    await page.route("**/api/v1/auth/reset-password", (route) =>
      route.fulfill({ status: 400, json: { error: "invalid or expired reset token" } }),
    );
    await page.goto("/reset-password?token=stale");
    await page.getByRole("textbox", { name: "New password", exact: true }).fill("Passw0rd!x");
    await page.getByRole("textbox", { name: "Confirm new password" }).fill("Passw0rd!x");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByRole("heading", { name: "This link cannot be used" })).toBeVisible();
  });

  test("a successful reset sends the user back to sign in", async ({ page }) => {
    await useLanguage(page, "en");
    let sent: { token?: string; password?: string } | undefined;
    await page.route("**/api/v1/auth/reset-password", async (route) => {
      sent = route.request().postDataJSON();
      await route.fulfill({ status: 200, json: { message: "ok" } });
    });
    await page.goto("/reset-password?token=good-token");
    await page.getByRole("textbox", { name: "New password", exact: true }).fill("Passw0rd!x");
    await page.getByRole("textbox", { name: "Confirm new password" }).fill("Passw0rd!x");
    await page.getByRole("button", { name: "Change password" }).click();

    await expect(page).toHaveURL(/\/login/, { timeout: 10_000 });
    expect(sent).toEqual({ token: "good-token", password: "Passw0rd!x" });
  });
});
