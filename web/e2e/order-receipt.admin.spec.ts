import { test, expect, type Page } from "@playwright/test";
import type { AdminOrderAudit, OrderReceipt } from "../src/api/types";
import { mockSession } from "./subscription-domain-fixtures";

const orderId = "d28dbc6b-fa14-44d9-84f0-eea26a3c7e19";
const userId = "00000000-0000-4000-8000-000000000001";
const receiptPath = `/api/v1/admin/users/${userId}/orders/${orderId}/receipt`;
const receipt: OrderReceipt = {
  order_id: orderId, plan_name: "Receipt plan", duration_days: 30,
  price_cents: 0, discount_cents: 500, promotion_name: "Welcome offer",
  currency: "usd", provider: "stripe", email: "receipt@example.test",
  created_at: "2026-10-26T12:00:00Z", paid_at: "2026-10-26T12:01:00Z",
  receipt_url: "https://invoice.stripe.com/i/fixture",
};
const audit: AdminOrderAudit = {
  order: {
    id: orderId, user_id: userId, user_email: receipt.email, user_name: "Receipt user",
    plan_id: "00000000-0000-4000-8000-000000000002", plan_name: receipt.plan_name,
    duration_days: 30, price_cents: 0, discount_cents: 500, status: "paid",
    provider: "stripe", provider_session_id: "cs_fixture", paid_by: "",
    created_at: "2026-10-26T12:00:00Z", paid_at: "2026-10-26T12:01:00Z",
    cancelled_at: null, expires_at: null, expired_at: null,
  },
  request_context: null, grant: null, payment_events: [],
};

async function receiptFixture(page: Page, failFirst = false) {
  await mockSession(page, "admin");
  await page.route("**/src/api/**", route => route.continue());
  let receiptRequests = 0;
  await page.route("**/api/v1/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path === `/api/v1/admin/orders/${orderId}/audit`) {
      return route.fulfill({ json: audit });
    }
    if (path === receiptPath) {
      receiptRequests++;
      if (failFirst && receiptRequests === 1) {
        return route.fulfill({ status: 502, json: { error: "receipt unavailable" } });
      }
      return route.fulfill({ json: receipt });
    }
    if (path.endsWith("/stats/alerts")) {
      return route.fulfill({ json: { total: 0, items: [] } });
    }
    return route.fulfill({ json: {} });
  });
  await page.goto(`/admin/orders/${orderId}`);
}

test("admin order detail opens the paid receipt and Stripe invoice link", async ({ page }) => {
  await receiptFixture(page);
  const response = page.waitForResponse(r => new URL(r.url()).pathname === receiptPath);
  await page.getByRole("button", { name: "View receipt", exact: true }).click();
  expect((await response).ok()).toBe(true);
  const dialog = page.getByRole("dialog", { name: "Receipt", exact: true });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(orderId, { exact: true })).toBeVisible();
  await expect(dialog.getByText(receipt.email, { exact: true })).toBeVisible();
  await expect(dialog.getByText(receipt.promotion_name ?? "", { exact: true })).toBeVisible();
  const invoice = dialog.getByRole("link", { name: "View Stripe invoice" });
  await expect(invoice).toHaveAttribute("href", receipt.receipt_url ?? "");
  await expect(invoice).toHaveAttribute("target", "_blank");
  await page.screenshot({ path: "/tmp/proxima-admin-receipt-desktop.png" });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(dialog).toBeVisible();
  await page.screenshot({ path: "/tmp/proxima-admin-receipt-mobile.png" });
});

test("admin receipt failure shows retry instead of an empty receipt", async ({ page }) => {
  await receiptFixture(page, true);
  await page.getByRole("button", { name: "View receipt", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Receipt", exact: true });
  await expect(dialog.getByText("Couldn't load the receipt.", { exact: true })).toBeVisible();
  await expect(dialog.getByText(orderId, { exact: true })).toHaveCount(0);
  const response = page.waitForResponse(r => new URL(r.url()).pathname === receiptPath);
  await dialog.getByRole("button", { name: "Retry", exact: true }).click();
  expect((await response).ok()).toBe(true);
  await expect(dialog.getByText(orderId, { exact: true })).toBeVisible();
});
