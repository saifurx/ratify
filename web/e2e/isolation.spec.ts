import { expect, test } from "@playwright/test";
import { login, unique } from "./helpers";

// Rep A and rep B are in different teams of the same tenant; tenant B is a
// different organisation entirely.
test("a rep never sees another rep's account in the list, the detail URL (404) or search", async ({ browser }) => {
  const nameB = unique("Secret-B-Account");

  const ctxB = await browser.newContext();
  const pageB = await ctxB.newPage();
  await login(pageB, "a-rep3");
  await pageB.goto("/accounts");
  await pageB.getByRole("button", { name: "New account" }).click();
  await pageB.getByLabel("Name").fill(nameB);
  await pageB.getByRole("button", { name: "Create account" }).click();
  await expect(pageB.getByText("Account created")).toBeVisible();
  await pageB.goto("/accounts");
  await pageB.getByRole("link", { name: nameB }).click();
  const detailUrl = pageB.url();
  await expect(pageB.getByTestId("account-name")).toHaveText(nameB);

  const ctxA = await browser.newContext();
  const pageA = await ctxA.newPage();
  await login(pageA, "a-rep1");

  await pageA.goto("/accounts");
  await expect(pageA.getByRole("link", { name: nameB })).toHaveCount(0);

  const res = await pageA.goto(detailUrl);
  expect(res?.status()).toBe(404);
  await expect(pageA.getByText("does not exist, or you do not have access")).toBeVisible();

  await pageA.goto("/");
  await pageA.getByLabel("Search").fill(nameB.slice(0, 12));
  await expect(pageA.getByText("No results")).toBeVisible();

  // while rep B (and the admin) can find it
  await pageB.goto("/");
  await pageB.getByLabel("Search").fill(nameB.slice(0, 12));
  await expect(pageB.getByRole("option").filter({ hasText: nameB })).toBeVisible();
  await ctxA.close();
  await ctxB.close();
});

test("a manager sees their team's records but not the other team's", async ({ page }) => {
  await login(page, "a-manager1"); // East: rep1, rep2
  await page.goto("/accounts");
  const owners = await page.locator("tbody tr td:nth-child(4)").allTextContents();
  expect(owners.length).toBeGreaterThan(0);
  for (const o of owners) expect(["Riley Rep (East) (a)", "Robin Rep (East) (a)", "You"].some((n) => o.includes(n.split(" (")[0]) || o === n)).toBeTruthy();
});

test("the browser cannot choose the tenant: injected headers, query and body are ignored or refused", async ({ page }) => {
  await login(page, "a-rep1");
  const whoami = await page.request.get("/api/bff/whoami", { headers: { "X-Tenant-Id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Authorization: "Bearer forged" } });
  expect(whoami.status()).toBe(200);
  expect((await whoami.json()).tenant_id).toBe("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa");

  const q = await page.request.get("/api/bff/accounts?tenant_id=bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb");
  expect(q.status()).toBe(400);
  const body = await page.request.post("/api/bff/accounts", { data: { name: "x", tenant_id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" } });
  expect(body.status()).toBe(400);
  expect((await body.json()).error).toMatch(/tenant_id is derived/);
  const nested = await page.request.post("/api/bff/accounts", { data: { name: "x", meta: { Tenant_ID: "b" } } });
  expect(nested.status()).toBe(400);
  // routes that are not part of the UI surface are not proxied
  expect((await page.request.post("/api/bff/inbound/leads", { data: { email: "a@b.co" } })).status()).toBe(404);
});

test("unauthenticated visitors are redirected to login and the BFF answers 401", async ({ page, request }) => {
  await page.goto("/accounts");
  await expect(page).toHaveURL(/\/login$/);
  expect((await request.get("/api/bff/accounts")).status()).toBe(401);
});

test("the other tenant's data is a different world", async ({ page }) => {
  await login(page, "b-admin");
  await page.goto("/accounts");
  const names = await page.locator("tbody tr td:first-child a").allTextContents();
  expect(names.length).toBeGreaterThan(0);
  await page.getByRole("link", { name: "Dashboard" }).click();
  await expect(page.getByText(/open deals worth/)).toBeVisible();
});
