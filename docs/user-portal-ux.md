# User portal UX

The user home is `/portal/dashboard`, including after sign-in and when following the product identity. It uses the existing Cloudscape shell and components; no second UI library, bespoke palette, or layout stylesheet is added.

## First-screen hierarchy

1. **My plan:** plan name and actual account status, monthly data used and allowance, remaining quota, days until expiry and expiry date, and registered/online device counts. Zero quota/caps mean unlimited rather than zero available. Distinct source addresses remain in a collapsed concurrency explanation, not mislabeled as devices.
2. **Connect a device:** create a device without leaving the dashboard, immediately select the returned device, and copy its subscription URL or open a QR code. The same selected device, client format and public domain determine both URL and QR. Creation responses do not contain the subscription token: reload the authoritative device list before exposing a URL, and show a retryable state if it is unavailable. Explicit dashboard refresh also reloads devices and public domains. Preserve encoded routes and unrelated query parameters when replacing host/format. The default public domain takes precedence over display order; retain the existing URL when public domains cannot be loaded. Explain importing and keeping the URL private. Suspended/expired/missing plans and reached device limits disable creation with explanatory copy.
3. **Service updates:** server online counts and a compact preview use only the user-visible node API, never admin metrics or invented latency. The three newest active, unexpired announcements are readable without blocking first entry; users explicitly open details or follow the full list.

## Purchase flow

Available plans remain visible to existing subscribers. Each purchasable card has a labeled duration Select, the exact returned total price and supported savings, and a single order action. Review the selected plan/duration/price in a confirmation modal before creating the order. Order creation is not payment; retain the existing provider checkout, pending-order block, promotions, cancellation and webhook grant behavior. Plans are one-off prepaid purchases, not automatic renewals. Traffic allowances are monthly, not the entire prepaid duration.

## States and accessibility

Dashboard requests load independently. Failure to load summary must show a retryable error, never a false no-plan state. Node or announcement failures cannot hide plan/device content. Warn at 80% quota usage and within seven days of expiry. Keep Cloudscape labeled controls, status text, focus/Modal behavior, light/dark surfaces, and responsive Grid/ColumnLayout reflow. Narrow layouts stack panels without horizontal document scrolling. Copy feedback uses Cloudscape CopyToClipboard.

Normal announcements no longer automatically open the layout-wide popup. Detailed device management and the existing public-domain reachability test remain available through the manage-devices link. A user-facing sign-out action removes only the user session token.

## Verification

Focused mocked browser tests in `web/e2e/user-dashboard.spec.ts` cover overview, connection, error/empty/expiry/limit states, announcement filtering, purchase controls and narrow/light/dark layouts without using real accounts or purchasing anything. The existing subscription-domain tests exercise the shared URL helper's preserved behavior. Aside verified rendering with fake data on an isolated read-only preview. Live-account visual review remains unverified because the user's browser had no authenticated user session and redirected to `/login`.

Verification completed: production build and scoped ESLint passed; locale parity passed for English, Korean and Chinese. All 14 new mocked scenarios passed (one ambiguous locator was corrected and rerun), and five existing subscription URL regressions passed. After visual/accessibility polish, the six affected dashboard/connection/purchase/narrow-theme scenarios passed again. Existing warnings remain for bundle size, extra legacy Chinese admin keys, and the untouched Devices loading effect dependency. No live payment or authenticated-account mutation was performed.

## Specification graph compatibility

The existing graph impact for `REQ-028` and `REQ-030` was computed and baseline validation passed. Existing constraints remain unchanged: one-off prepaid purchases, monthly quota resets, payment confirmation, and webhook idempotency. This independent dashboard UX does not repurpose a resolved plan or claim backend delivery.

The installed spec-graph CLI is version 0.3.4 and requires caller-supplied IDs for `entity add`, whereas the installed operator skill requires generated IDs and forbids inventing them. Registration of a new independent change is therefore blocked pending a compatible CLI update; no IDs or graph entities were fabricated.
