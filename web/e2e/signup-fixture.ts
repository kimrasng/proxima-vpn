import { expect, type APIRequestContext, type Page } from "@playwright/test";

let adminToken: string | undefined;

// Only the explicitly enabled in-memory CI mailer exposes this admin-protected
// endpoint. Real SMTP deployments never expose verification codes.
export async function signupCode(request: APIRequestContext, email: string): Promise<string> {
  if (!adminToken) {
    const login = await request.post("/api/v1/admin/auth/login", { data: {
      email: process.env.E2E_ADMIN_EMAIL || "admin@example.com",
      password: process.env.E2E_ADMIN_PASSWORD || "Admin1234!",
    } });
    expect(login.ok(), "admin fixture login").toBe(true);
    adminToken = ((await login.json()) as { token: string }).token;
  }
  const response = await request.get(`/api/v1/__test__/signup-email-code?email=${encodeURIComponent(email)}`, {
    headers: { Authorization: `Bearer ${adminToken}` },
  });
  expect(response.ok(), "test mailbox lookup (requires ENABLE_TEST_EMAIL_CODE_LOOKUP)").toBe(true);
  return ((await response.json()) as { code: string }).code;
}

export async function confirmSignup(page: Page, email: string) {
  const input = page.getByRole("textbox", { name: "6-digit verification code" });
  await expect(input).toBeVisible();
  await input.fill(await signupCode(page.request, email));
  await page.getByRole("button", { name: "Verify and create account" }).click();
}
