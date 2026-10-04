import { test, expect } from "@playwright/test";
import { mockSession } from "./subscription-domain-fixtures";

test.use({ storageState: { cookies: [], origins: [] } });
test("pending UUID ban exposes exactly which Exit has not acknowledged without release",async({page})=>{
 await mockSession(page,"admin","ko");
 await page.route("**/src/api/**",route=>route.continue());
 await page.route("**/api/v1/admin/uuid-evictions",route=>route.fulfill({json:[{
  device_uuid:"00000000-0000-4000-8000-000000000001",epoch:"00000000-0000-4000-8000-000000000002",state:"pending",requested_at:"2026-10-01T00:00:00Z",confirmed_at:null,
  required_node_ids:["entry-a","entry-b"],acknowledged_node_ids:["entry-a"],pending_node_ids:["entry-b"],node_names:{"entry-a":"일본 출구","entry-b":"미국 출구"}
 }]}));
 await page.route("**/api/v1/admin/uuid-evictions/*/retry",route=>route.fulfill({json:{state:"pending",message:"ban retained"}}));
 await page.goto("/admin/uuid-evictions");
 await expect(page.getByText("일본 출구 — 종료 확인")).toBeVisible();
 await expect(page.getByText("미국 출구 — 확인 대기")).toBeVisible();
 await expect(page.getByText(/재시도는 차단을 해제하지 않습니다/)).toBeVisible();
 await page.getByRole("button",{name:"확인 재시도"}).click();
 await expect(page.getByText("출구 확인 대기")).toBeVisible();
});
