import { test, expect } from "@playwright/test";

import { chain, entry, exit, mockTopology } from "./managed-chain-fixtures";

test.use({ storageState: { cookies: [], origins: [] } });

test("plan policy advertises per-device directional ceilings without claiming deployment readiness", async ({ page }) => {
 await mockTopology(page);
 const plan={id:"00000000-0000-0000-0000-000000000001",name:"Device plan",node_group_id:"access",speed_limit:100,duration_days:30,max_devices:2,max_concurrent:2,is_active:true,created_at:"2026-01-01T00:00:00Z",purchasable:false,prices:[],features:[]};
 await page.route("**/api/v1/admin/plans",r=>r.fulfill({json:[plan]}));
 await page.route(`**/api/v1/admin/plans/${plan.id}`,r=>r.fulfill({json:plan}));
 await page.route(`**/api/v1/admin/plans/${plan.id}/routes`,r=>r.fulfill({json:{chain_ids:[chain.id],node_group_id:"access",speed_limit:100,speed_enforcement:"device_global_v1",warnings:["device_bandwidth_requires_current_agent_ack"]}}));
 await page.route("**/api/v1/admin/node-groups",r=>r.fulfill({json:[{id:"access",name:"Access"}]}));
 await page.route("**/api/v1/admin/node-chains",r=>r.fulfill({json:[chain]}));
 await page.route("**/api/v1/admin/nodes",r=>r.fulfill({json:[entry,{...exit,shaping_mode:"device_global_v1",shaping_ok:true}]}));
 await page.goto("/admin/plans");
 await page.getByRole("button",{name:"Manage routes",exact:true}).click();
 await expect(page.locator("#plan-routes")).toContainText("Per device: upload and download each up to 100 Mbps");
 await expect(page.getByText(/Requires a current agent and acknowledged configuration/)).toBeVisible();
 await expect(page.getByText("Per-device bandwidth enforcement is not implemented.",{exact:false})).toHaveCount(0);
});
