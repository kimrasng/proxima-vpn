# Proxima VPN Admin Design System

## 1. Atmosphere & Identity

A restrained operations console. Administrators should distinguish topology, service state, and destructive actions at a glance. The existing Cloudscape application shell, form controls, tables, and status indicators are the visual contract; do not introduce a second component system.

## 2. Color

Use Cloudscape semantic tokens through `@cloudscape-design/components` and `@cloudscape-design/design-tokens`. The application already supports Cloudscape light and dark modes. Never introduce raw hex or an additional accent palette. Use Cloudscape status variants for success, warning, and error, and semantic text colors for explanatory copy.
In the node list, role badges use Cloudscape's green for entry relay, blue for exit, and grey for dual-role nodes. The role text remains visible so color is never the only distinction; service health uses the separate status indicator.

## 3. Typography

Use Cloudscape's default type scale: page `Header variant="h1"`, section `Header variant="h2"`, form `FormField`, body `Box variant="p"`, and secondary `Box variant="small"`. No custom font families or pixel font sizes.

## 4. Spacing & Layout

The `AppLayoutToolbar` owns navigation and content scrolling. Pages use `ContentLayout`, `SpaceBetween`, `Container`, and `Table` with Cloudscape's named sizes (`xs`, `s`, `m`, `l`). Modal forms stack labeled controls and remain usable in the narrow layout without fixed-width CSS.

## 5. Components

### Node provisioning wizard
- Structure: Cloudscape `Wizard` nested in a `Modal`, with `FormField` inputs and a read-only install command result.
- States: initial, invalid-field, submitting, API failure, and completed command. Role selection occurs before generating a token and is explicit in the review step.
- Accessibility: labeled native Cloudscape controls, keyboard navigation, focus managed by Modal.
- Copy: role tiles describe what the server does for a customer in plain language, reserving Xray, tunnels, and chains for advanced settings. The step heading owns the section title; do not repeat it inside the container.

### Admin resource table
- Structure: Cloudscape `Table` in a `ContentLayout` with header actions and an explicit empty state.
- States: loading, empty, populated, error, and busy action. Destructive actions use a confirmation Modal.
- Accessibility: named column headers, labeled action buttons, semantic status indicators.
- Node views: Cloudscape `Tabs` switch among all servers, Entry servers and Exit servers; combined-role servers participate in both role views. The active role is URL-backed. Changing tabs retains search/status/region filters and resets pagination only. Subscription-group assignment is a separate filter, not a claim that a server is online or a route is ready. The KPI strip summarizes all nodes. Default columns prioritize role, location, availability and links to route management; technical endpoint and resource fields remain available through column preferences.
- Alerts: the four counters reflow using Cloudscape `ColumnLayout`. Summary alerts use Cloudscape `Cards` so their descriptions and destination links stay together. The detailed issue list retains sortable `Table` at roomy widths and switches to `Cards` when its own container is narrow. Both presentations share the same filters, pagination, lifecycle status, and action menu; only the visible presentation participates in layout or keyboard navigation. Filters wrap rather than forcing the page wider. The application shell remains the vertical scroll owner; neither card presentation creates a nested vertical scroller.
- Activity: keep the existing sortable table at roomy widths and use a card presentation for narrow content, retaining severity, actor, event, target, and time in each record. Use the same filter and pagination state in either presentation. Count tiles follow the same Cloudscape reflow as Alerts.
- Activity investigation: four read-only Cloudscape summary cards (total, critical, warning, info) summarize the fetched time window, independent of property filters. A separate severity Select filters records alongside a PropertyFilter for search and resource/event chips and a time selector. Pagination sits beside the log heading rather than occupying a separate tall row. The activity page may use a 1600px shell content ceiling; other admin pages retain 1200px. A compact selectable table opens a Container detail panel in a 2:1 grid only when its container exceeds 1550px, so time, severity, actor and event columns have readable floors; below that the detail panel stacks below the full-width log, and below 1050px the log becomes cards. Explicitly selecting a record in the stacked layout scrolls its details into view; closing restores focus to the selection control. Initial selection never scrolls. Spacing uses Cloudscape `space-scaled-m`; long identifiers and raw JSON wrap. Details expose only real API fields, link only known node/user routes, and use CopyToClipboard for the real entry JSON. The 200-entry scope is explicit. Selection clears when filters exclude the event; auto-refresh can be paused. Cloudscape owns selected colors, focus, and control states.

### Topology form
- Refinement contract: serve operators scanning a route before publishing it. Section headings are sentence-case `Box` h3 elements at Cloudscape `body-s` size and bold weight, without decorative icons. Use `m` between sections and `xs` inside them. The normal-size, secondary-colored route arrow is decorative; selectors align at the top independently of metadata length. Host and listener port use a 2:1 grid, and advanced controls use three equal columns with helper text below inputs; advanced settings start collapsed in both create and edit. Local CSS uses Cloudscape spacing tokens (`xs`/8px, `s`/12px, `xl`/32px fallbacks), with a 36rem modal-content breakpoint for single-column reflow. Do not target internal Cloudscape selectors.
- Connection table: Entry-centered port map includes direct routes rather than hiding them. Select one Entry to inspect every public port and add multiple Exits in a batch. Endpoint, destination, assigned plans, configuration checks and enabled state are separate columns. Configuration checks are explicitly not proof of subscription eligibility or applied policy; missing acknowledgments must never display a ready badge. Retain legacy presentation edits and row enable/disable actions. Batch creation previews allocations before committing; preview does not reserve ports and a conflict retains form values. Plan route assignment uses a separate saved-plan panel and does not overwrite other plans sharing the original group.
- Validation: browser E2E and visual QA are required for the topology form, including desktop/narrow layouts and Cloudscape light/dark rendering, alongside build, lint, locale parity, and source diagnostics.
- Structure: the creation modal follows the route/connection details/access rights/advanced sections of the supplied operations-console reference, but avoids nested boxes. The modal is the single surface; one introductory sentence, Cloudscape section titles, and vertical spacing establish hierarchy without repeating field instructions in every heading. Entry and exit selectors sit side by side with a directional arrow and actual node health/location metadata, stacking vertically when the modal narrows. Connection name is first, followed by the read-only managed Entry hostname and exit listener port. Derive the hostname only from the selected node's `entry_hostname`, display its `entry_dns_status`, and omit `entry_host` from explicit Entry create/update requests; never fall back to node IP or the saved chain host. Selecting an exit defaults its listener port. A suggested name follows the pair until the operator edits it. Explain managed host ownership and that no access groups means unpublished. Subscription groups use a searchable `Multiselect`; entry port, transport, and priority live in a collapsed unboxed `ExpandableSection`. The edit modal retains the same field hierarchy and its immutable-field guidance. Legacy pool links retain their saved, editable host and DNS-only guidance.
- States: no forwarding entry node or exit, unselected nodes, automatically populated fields, manually overridden name (or legacy host), missing managed hostname, submitting, validation conflict, and success. A missing managed hostname has an inline explanation and blocks creation without discarding values; pending/error/conflict DNS states do not block a stored hostname. Presentation-only explicit edits remain possible without a managed hostname. A separate edit form changes only API-supported fields. Legacy pool links remain visible but cannot be newly created or converted.
- Accessibility: error feedback next to its field, keyboard-reachable controls, and visible text for node status so color is not its only cue. The reference marks access groups required, but existing publication behavior permits zero groups (an unpublished link); keep the actual API contract rather than implying a new validation rule.

## 6. Motion & Interaction

Use Cloudscape's existing focus, hover, modal, and button feedback. No custom animation. Respect reduced-motion preferences as handled by Cloudscape. Refresh resource lists after successful mutations; preserve form values on API errors.

## 7. Depth & Surface

Use Cloudscape Containers and Modals for the established elevation hierarchy, and its own light/dark surface treatments. Do not add custom shadows, borders, or gradients.

## 8. Accessibility Constraints & Accepted Debt

Target WCAG 2.2 AA through Cloudscape controls: visible focus, readable status text instead of color alone, form labels, actionable error messages, and single-column reflow at narrow widths. Existing admin pages contain older inline layout styling outside this feature's scope; this feature adds none.
