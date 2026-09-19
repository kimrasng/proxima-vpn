import { test, expect, type Page } from "@playwright/test";

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

// A unique suffix per run so repeated runs against the same database don't
// collide on unique names.
const uniq = () => Math.random().toString(36).slice(2, 8);

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
      { nav: "Plan Requests", urlPart: "/admin/plan-requests", heading: /Request/i },
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
