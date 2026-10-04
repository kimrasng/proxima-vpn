import { test, expect, type Locator, type Page } from "@playwright/test";

// Browser e2e for the admin panel, driving the real UI against a real
// api-server. This covers the layer that `npm run build` + `go test` both
// miss: whether the pages render, whether forms send what the API actually
// accepts, and whether the response makes it back into the UI.

const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@example.com";

// The app auto-detects language (see src/i18n/index.ts); pin it to English so
// assertions can match on labels deterministically.
async function useEnglish(page: Page) {
  await page.addInitScript(() => {
    window.localStorage.setItem("i18nextLng", "en");
  });
}

async function selectAllOrders(page: Page, optionName: string) {
  const ordersResponse = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      response.request().method() === "GET" &&
      url.pathname === "/api/v1/admin/orders" &&
      !url.searchParams.has("status") &&
      response.status() >= 200 &&
      response.status() < 300
    );
  });

  await page.getByRole("option", { name: optionName, exact: true }).click();
  await ordersResponse;
}

// A unique suffix per run so repeated runs against the same database don't
// collide on unique names.
const uniq = () => Math.random().toString(36).slice(2, 8);

// What makes a clipped table acceptable: it really does overflow, scrolling
// reaches the final column, a visible scroll affordance is rendered, and the
// column that identifies the row stays on screen the whole way across.
//
// That last one is what makes the wide columns readable rather than merely
// reachable. These tables run ~1300px wider than a 375px viewport, so by the
// time the far-right columns are scrolled into view an unpinned first column has
// left the screen entirely - every cell on screen belongs to an order the reader
// can no longer name. `stickyColumns={{ first: 1 }}` keeps it in place. Hiding
// columns is not the alternative: the columns are the audit record.
//
// The affordance is Cloudscape's own sticky scrollbar, a sibling element - NOT a
// gutter on the wrapper. The wrapper sets `scrollbar-width: none` and the
// platform paints an overlay scrollbar, so measuring `offsetHeight -
// clientHeight` on the wrapper reports 0 even when the bar is plainly visible.
async function measureScrollRegion(region: Locator) {
  const wrapper = await region.evaluate((el) => {
    const before = el.scrollLeft;
    el.scrollLeft = el.scrollWidth;
    const headers = [...el.querySelectorAll("thead th")];
    const lastHeader = headers.at(-1);
    const lastColumnReachable = lastHeader
      ? lastHeader.getBoundingClientRect().right <= window.innerWidth + 1
      : false;
    // Measured at the far-right scroll extreme: unpinned, the first column's
    // box has been carried off the left edge by then.
    const firstHeader = headers.at(0);
    const firstHeaderBox = firstHeader?.getBoundingClientRect();
    const firstColumnPinned = firstHeaderBox
      ? firstHeaderBox.right > 0 && firstHeaderBox.left < window.innerWidth
      : false;
    el.scrollLeft = before;
    return {
      overflows: el.scrollWidth > el.clientWidth,
      lastColumnReachable,
      firstColumnPinned,
    };
  });

  const scrollbar = region
    .locator('xpath=following-sibling::div[contains(@class,"sticky-scrollbar")][1]')
    .first();
  const scrollbarVisible =
    (await scrollbar.count()) > 0 &&
    (await scrollbar.evaluate((el) => {
      const box = el.getBoundingClientRect();
      return getComputedStyle(el).display !== "none" && box.height > 0 && box.width > 0;
    }));

  return { ...wrapper, scrollbarVisible };
}

// The signed-in session comes from e2e/auth.setup.ts via storageState. These
// two tests need the *absence* of that session, so they opt out of it.
test.describe("admin authentication", () => {
  test.use({ storageState: { cookies: [], origins: [] } });

  test("unauthenticated visit to an admin page redirects to login", async ({ page }) => {
    await useEnglish(page);
    await page.goto("/admin/users");
    await expect(page).toHaveURL(/\/admin\/login/);
  });

  test("wrong credentials are rejected and do not navigate away", async ({ page }) => {
    await useEnglish(page);
    await page.goto("/admin/login");
    await page.getByRole("textbox", { name: "Email" }).fill(ADMIN_EMAIL);
    await page.getByRole("textbox", { name: "Password" }).fill("definitely-wrong-password");
    await page.getByRole("button", { name: "Sign In" }).click();

    // AdminLogin surfaces the API's own error text when there is one (see
    // AdminLogin.tsx), falling back to the translated message otherwise.
    await expect(
      page.getByText(/invalid credentials|Login failed/i).first(),
    ).toBeVisible();
    await expect(page).toHaveURL(/\/admin\/login/);
  });
});

test.describe("admin navigation", () => {
  test("the stored admin session lands on the dashboard", async ({ page }) => {
    await useEnglish(page);
    await page.goto("/admin/dashboard");
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  });

  test("every nav destination renders without an error state", async ({ page }) => {
    await useEnglish(page);
    await page.goto("/admin/dashboard");

    // Each admin page fetches on mount; a broken endpoint or a render crash
    // shows up here as a missing heading or a visible error flash.
    const pages: Array<{ nav: string; urlPart: string; heading: RegExp }> = [
      { nav: "Alerts & Actions", urlPart: "/admin/alerts", heading: /Alert/i },
      { nav: "Recent Activity", urlPart: "/admin/activity", heading: /Activity/i },
      { nav: "Nodes", urlPart: "/admin/nodes", heading: /Node/i },
      { nav: "Node Groups", urlPart: "/admin/node-groups", heading: /Node Group/i },
      { nav: "Plans", urlPart: "/admin/plans", heading: /Plan/i },
      { nav: "Users", urlPart: "/admin/users", heading: /User/i },
      { nav: "Connections", urlPart: "/admin/connections", heading: /Connection/i },
      { nav: "Announcements", urlPart: "/admin/announcements", heading: /Announcement/i },
      { nav: "Settings", urlPart: "/admin/settings", heading: /Settings/i },
    ];

    for (const p of pages) {
      // Scoped to the side nav: the breadcrumb of the page you are already on
      // carries the same accessible name, which makes a bare getByRole ambiguous.
      await page.locator(`nav a[href="${p.urlPart}"]`).click();
      await expect(page).toHaveURL(new RegExp(p.urlPart.replace(/\//g, "\\/")));
      await expect(page.getByRole("heading", { name: p.heading }).first()).toBeVisible();
      // Nothing on a freshly-loaded page should be reporting a load failure.
      await expect(page.getByText(/Failed to load/i)).toHaveCount(0);
    }
  });
});

test.describe("admin settings", () => {
  // This is the flow that was silently broken: the form submits booleans for
  // its toggles, and the backend used to reject the whole request with a 500,
  // so "Save" never actually saved. Assert the success path explicitly and
  // verify the value survives a reload.
  test("settings save succeeds and persists across a reload", async ({ page }) => {
    await useEnglish(page);
    await page.goto("/admin/settings");
    await expect(page.getByRole("heading", { name: /Settings/i }).first()).toBeVisible();

    const interval = String(3600 + Math.floor(Math.random() * 900));
    // Numeric settings render as <input type="number"> (spinbutton), not textbox.
    await page.getByRole("spinbutton", { name: "Update Interval" }).fill(interval);

    await page.getByRole("button", { name: "Save Settings" }).click();

    await expect(page.getByText(/Settings saved successfully/i)).toBeVisible();
    await expect(page.getByText(/Failed to save settings/i)).toHaveCount(0);

    await page.reload();
    await expect(page.getByRole("spinbutton", { name: "Update Interval" })).toHaveValue(interval);
  });

  test("self-registration toggle round-trips", async ({ page }) => {
    await useEnglish(page);
    await page.goto("/admin/settings");

    const toggle = page.getByRole("checkbox", { name: "Allow Self-Registration" });
    await expect(toggle).toBeVisible();

    const before = await toggle.isChecked();
    await toggle.click();
    await page.getByRole("button", { name: "Save Settings" }).click();
    await expect(page.getByText(/Settings saved successfully/i)).toBeVisible();

    await page.reload();
    await expect(page.getByRole("checkbox", { name: "Allow Self-Registration" }))
      .toBeChecked({ checked: !before });

    // Restore, so this test leaves the panel as it found it (and so a later
    // run of the user-registration spec isn't blocked by it).
    await page.getByRole("checkbox", { name: "Allow Self-Registration" }).click();
    await page.getByRole("button", { name: "Save Settings" }).click();
    await expect(page.getByText(/Settings saved successfully/i)).toBeVisible();
  });
});

test.describe("admin order audit", () => {
  // The audit view is reached by clicking an order's plan in the list, so this
  // needs a real order to exist. A fresh database legitimately has none; the
  // test reports that as a skip rather than passing vacuously or failing on
  // data it does not own.
  test("opening an order from the list shows its audit sections", async ({ page }) => {
    await useEnglish(page);
    await page.goto("/admin/orders");
    await expect(page.getByRole("heading", { name: /Orders/i }).first()).toBeVisible();

    // The list defaults to the pending filter; widen it so any order qualifies.
    await page.getByRole("button", { name: "Awaiting payment" }).click();
    await selectAllOrders(page, "All");
    await expect(page.getByText(/Failed to load orders/i)).toHaveCount(0);

    const auditLink = page.locator('table a[href^="/admin/orders/"]').first();
    if ((await auditLink.count()) === 0) {
      test.skip(true, "no plan orders exist in this database to audit");
      return;
    }

    await auditLink.click();
    await expect(page).toHaveURL(/\/admin\/orders\/[0-9a-f-]{36}/);

    await expect(page.getByRole("heading", { name: "Order Audit" })).toBeVisible();
    await expect(page.getByText(/Failed to load the order audit record/i)).toHaveCount(0);

    // The four sections the endpoint feeds. Request context and grant each
    // render either their values or their own "not captured" notice, so the
    // heading must be present regardless of what the order carries.
    for (const section of ["Order Summary", "Request Context", "Plan Grant", "Payment Events"]) {
      await expect(page.getByRole("heading", { name: section })).toBeVisible();
    }

    // The fingerprint must never read as something that grants access.
    await expect(page.getByText(/grants nothing/i).first()).toBeVisible();

    // Scoped to the breadcrumb, whose links sit in an ordered list: the side nav
    // carries an "Orders" link with the same accessible name, so an unscoped
    // getByRole would be ambiguous.
    await page.locator('ol a[href="/admin/orders"]').first().click();
    await expect(page).toHaveURL(/\/admin\/orders$/);
  });

  // Both order tables are wider than the panel's content column at every
  // viewport, so Cloudscape turns their wrapper into a focusable
  // `role="region"` a keyboard user arrow-keys sideways. That region is only
  // announced if the table passes `ariaLabels.tableLabel` - without it the
  // region has no accessible name. Hiding columns is not the alternative: the
  // columns are the audit record.
  //
  // Pinning is asserted at 768px, not 375px: Cloudscape disables stickyColumns
  // unless the pinned column plus 148px of scrollable space fits the wrapper,
  // which a 325px wrapper cannot do. Narrowing the identity column to buy the
  // pin would truncate the email and received-at values that name each row, so
  // at 375px the guarantee is reachability, and pinning resumes above it.
  test("the scrollable order tables name their scroll region", async ({ page }) => {
    await useEnglish(page);
    // 375px, where the overflow is largest and the overlay scrollbar leaves no
    // visual hint at all.
    await page.setViewportSize({ width: 375, height: 812 });

    await page.goto("/admin/orders");
    await expect(page.getByRole("heading", { name: /Orders/i }).first()).toBeVisible();

    const listRegion = page.getByRole("region", { name: "Orders" });
    await expect(listRegion).toBeVisible();
    expect(await measureScrollRegion(listRegion)).toMatchObject({
      overflows: true,
      scrollbarVisible: true,
      lastColumnReachable: true,
    });

    const auditLink = page.locator('table a[href^="/admin/orders/"]').first();
    if ((await auditLink.count()) === 0) {
      test.skip(true, "no plan orders exist in this database to audit");
      return;
    }
    await auditLink.click();
    await expect(page.getByRole("heading", { name: "Order Audit" })).toBeVisible();

    const eventsRegion = page.getByRole("region", { name: "Payment Events" });
    await expect(eventsRegion).toBeVisible();
    expect(await measureScrollRegion(eventsRegion)).toMatchObject({
      overflows: true,
      scrollbarVisible: true,
      lastColumnReachable: true,
    });

    // Both tables' pins, at a width where the library grants them. Loaded fresh
    // rather than resized into: Cloudscape decides stickiness on mount, so a
    // mid-test resize leaves the earlier decision in place.
    const auditUrl = page.url();
    await page.setViewportSize({ width: 768, height: 812 });

    await page.goto(auditUrl);
    await expect(page.getByRole("heading", { name: "Order Audit" })).toBeVisible();
    expect(
      await measureScrollRegion(page.getByRole("region", { name: "Payment Events" })),
    ).toMatchObject({ lastColumnReachable: true, firstColumnPinned: true });

    await page.goto("/admin/orders");
    await expect(page.getByRole("heading", { name: /Orders/i }).first()).toBeVisible();
    expect(await measureScrollRegion(page.getByRole("region", { name: "Orders" }))).toMatchObject({
      lastColumnReachable: true,
      firstColumnPinned: true,
    });
  });

  // A bare `toLocaleString()` follows the browser locale, not the language the
  // admin picked, so this column read "9/21/2026, 7:08:44 PM" on a Chinese page
  // while the audit detail beside it read "2026/9/21 19:08:44".
  //
  // Matched by shape, not a fixed string: the timestamp belongs to the database.
  test("the order list renders its dates in the selected language", async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem("i18nextLng", "zh");
    });

    await page.goto("/admin/orders");
    await page.getByRole("button", { name: "待付款" }).click();
    await selectAllOrders(page, "全部");

    const auditLink = page.locator('table a[href^="/admin/orders/"]').first();
    if ((await auditLink.count()) === 0) {
      test.skip(true, "no plan orders exist in this database to audit");
      return;
    }

    const orderedAt = page.locator("table tbody tr").first().locator("td").nth(9);
    // zh-CN: year first, 24-hour clock, no AM/PM marker.
    await expect(orderedAt).toHaveText(/^\d{4}\/\d{1,2}\/\d{1,2}\s+\d{1,2}:\d{2}:\d{2}$/);
    await expect(orderedAt).not.toHaveText(/AM|PM/);
  });

  test("a non-existent order id reports not found rather than a blank page", async ({ page }) => {
    await useEnglish(page);
    // A well-formed UUID no order uses: the endpoint answers 404, which the
    // page must surface as an error state with a way back.
    await page.goto("/admin/orders/00000000-0000-4000-8000-000000000000");

    await expect(page.getByRole("heading", { name: "Order Audit" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Back to orders" })).toBeVisible();
  });
});

test.describe("admin CRUD", () => {
  test("create a node group, then a plan that uses it, then clean up", async ({ page }) => {
    await useEnglish(page);

    const groupName = `e2e-ui-group-${uniq()}`;
    const planName = `e2e-ui-plan-${uniq()}`;

    // --- node group ---
    await page.goto("/admin/node-groups");
    await page.getByRole("button", { name: "Create Group" }).click();
    await page.getByRole("textbox", { name: "Group Name" }).fill(groupName);
    await page.getByRole("button", { name: "Save", exact: true }).click();

    await expect(page.getByText(groupName)).toBeVisible();

    // --- plan referencing that group ---
    await page.goto("/admin/plans");
    await page.getByRole("button", { name: "Create Plan" }).click();
    await page.getByRole("textbox", { name: "Plan Name" }).fill(planName);

    // duration / devices are required and must be positive (see
    // admin_plan.go's validation) - the UI must send them for the create to
    // succeed at all. They're number inputs, and the form pre-fills them, so
    // set them explicitly rather than relying on the defaults.
    await page.getByRole("spinbutton", { name: "Duration (days)" }).fill("30");
    await page.getByRole("spinbutton", { name: "Max Devices" }).fill("3");

    // Node group is a select; pick the one just created.
    await page.getByRole("button", { name: "Node Group" }).click();
    await page.getByRole("option", { name: groupName }).click();

    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText(planName)).toBeVisible();
    await expect(page.getByText(/Failed to (create|save)/i)).toHaveCount(0);
  });
});
