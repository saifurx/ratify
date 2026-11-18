import { expect, test } from "@playwright/test";
import { login } from "./helpers";

// Every screen renders with the seed-demo dataset (no error boundary, no console errors).
const SCREENS: [string, RegExp][] = [
  ["/", /Dashboard/],
  ["/accounts", /Accounts/],
  ["/contacts", /Contacts/],
  ["/opportunities", /Pipeline: Sales/],
  ["/leads", /Lead inbox/],
];

for (const persona of ["a-admin", "a-manager1", "a-rep1", "a-finance", "b-rep1"]) {
  test(`all screens render for ${persona}`, async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    page.on("console", (m) => m.type() === "error" && !m.text().includes("Failed to load resource") && errors.push(m.text()));
    await login(page, persona);
    for (const [path, heading] of SCREENS) {
      const res = await page.goto(path);
      expect(res?.status(), path).toBe(200);
      await expect(page.getByRole("heading", { level: 1 }).first()).toHaveText(heading);
    }
    // detail screens
    await page.goto("/accounts");
    await page.locator("tbody a").first().click();
    await expect(page.getByTestId("account-name")).toBeVisible();
    for (const tab of ["Contacts", "Opportunities", "Activity"]) {
      await page.getByRole("link", { name: new RegExp(`^${tab}`) }).click();
      await expect(page.getByRole("navigation", { name: "Account sections" })).toBeVisible();
    }
    await page.goto("/contacts");
    await page.locator("tbody a").first().click();
    await expect(page.getByTestId("contact-name")).toBeVisible();
    expect(errors).toEqual([]);
  });
}

test("dashboard shows pipeline value by stage, my open deals and recent activity", async ({ page }) => {
  await login(page, "a-rep1");
  await expect(page.getByTestId("pipeline-by-stage").locator("li")).toHaveCount(6);
  await expect(page.getByRole("heading", { name: "My open deals" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Recent activity" })).toBeVisible();
});

test("role-based rendering mirrors the API", async ({ page }) => {
  await login(page, "a-finance");
  await page.goto("/accounts");
  await expect(page.getByRole("button", { name: "New account" })).toHaveCount(0);
  await page.goto("/settings");
  await expect(page.getByText("Admins only")).toBeVisible();
  await page.goto("/opportunities");
  await expect(page.getByText("Read-only view.")).toBeVisible();
  await expect(page.getByLabel("Change stage")).toHaveCount(0);
});

test("admins see Settings; reps do not", async ({ page }) => {
  await login(page, "a-admin");
  await expect(page.getByRole("link", { name: "Settings" })).toBeVisible();
  await page.goto("/settings");
  await expect(page.getByText("Custom fields")).toBeVisible();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await login(page, "a-rep1");
  await expect(page.getByRole("link", { name: "Settings" })).toHaveCount(0);
});

test("lead inbox: preliminary scores, and Convert is disabled with an explanation when consent is missing", async ({ page }) => {
  await login(page, "a-rep1");
  await page.goto("/leads");
  await expect(page.getByTestId("score").first()).toBeVisible();
  await expect(page.getByText("preliminary").first()).toBeVisible();
  const blocked = page.locator("tr[data-lead]").filter({ has: page.getByTestId("convert-reason") }).first();
  await expect(blocked.getByRole("button", { name: "Convert" })).toBeDisabled();
  await expect(blocked.getByTestId("convert-reason")).toContainText(/consent|suppression|revoked|contact/i);
  const ok = page.locator("tr[data-lead]").filter({ hasNot: page.getByTestId("convert-reason") }).filter({ has: page.getByRole("button", { name: "Convert" }) }).first();
  await expect(ok.getByRole("button", { name: "Convert" })).toBeEnabled();
});

test("converting an eligible lead creates an account and navigates to it", async ({ page }) => {
  await login(page, "b-rep1");
  await page.goto("/leads");
  const ok = page.locator("tr[data-lead]").filter({ hasNot: page.getByTestId("convert-reason") }).filter({ has: page.getByRole("button", { name: "Convert" }) }).first();
  await ok.getByRole("button", { name: "Convert" }).click();
  await expect(page.getByText(/Lead converted/)).toBeVisible();
  await expect(page).toHaveURL(/\/accounts\/[0-9a-f-]{36}/);
});
