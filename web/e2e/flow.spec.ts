import { expect, test } from "@playwright/test";
import { login, unique } from "./helpers";

test("login, create account, create opportunity, change stage, timeline updates", async ({ page }) => {
  const acct = unique("E2E Account");
  await login(page, "a-rep1");
  await expect(page.getByTestId("user-role")).toHaveText("rep");

  await page.goto("/accounts");
  await page.getByRole("button", { name: "New account" }).click();
  await page.getByLabel("Name").fill(acct);
  await page.getByLabel("Domain").fill("e2e-co.test");
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page.getByText("Account created")).toBeVisible();

  await page.goto("/accounts");
  await page.getByRole("link", { name: acct }).click();
  await expect(page.getByTestId("account-name")).toHaveText(acct);

  await page.getByRole("link", { name: /^Opportunities/ }).click();
  await page.getByRole("button", { name: "New opportunity" }).click();
  await page.getByLabel("Name").fill("E2E deal");
  await page.getByLabel("Amount (USD)").fill("12,500");
  await page.getByRole("button", { name: "Create opportunity" }).click();
  await expect(page.getByText("Opportunity created")).toBeVisible();
  const row = page.locator('tr[data-opportunity="E2E deal"]');
  await expect(row).toBeVisible();

  await row.getByLabel("Change stage").selectOption({ label: "Qualified" });
  await expect(page.getByText("Stage updated")).toBeVisible();

  await page.getByRole("link", { name: /^Activity/ }).click();
  await expect(page.getByTestId("timeline-item").filter({ hasText: "Stage changed: New → Qualified" })).toBeVisible();

  // log a note through the composer; it joins the timeline
  await page.getByLabel("Subject").fill("Spoke to the buyer");
  await page.getByRole("button", { name: "Log activity" }).click();
  await expect(page.getByText("Activity logged")).toBeVisible();
  await expect(page.getByTestId("timeline-item").filter({ hasText: "Spoke to the buyer" })).toBeVisible();
});

test("client-side validation and the API's own error messages are shown", async ({ page }) => {
  await login(page, "a-rep1");
  await page.goto("/accounts");
  await page.getByRole("button", { name: "New account" }).click();
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "Name is required" })).toBeVisible();

  await page.goto("/contacts");
  await page.getByRole("button", { name: "New contact" }).click();
  await page.getByLabel("Email").fill("not-an-email");
  await page.getByRole("button", { name: "Create contact" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "valid email" })).toBeVisible();
});

test("dragging a card to another stage moves the deal", async ({ page }) => {
  await login(page, "a-rep1");
  await page.goto("/opportunities");
  const board = page.getByTestId("board");
  const card = board.locator("li[data-opportunity]").first();
  const name = (await card.getAttribute("data-opportunity"))!;
  const from = await card.evaluate((el) => el.closest("section")!.getAttribute("data-stage"));
  const targetStage = from === "Proposal" ? "Negotiation" : "Proposal";
  await card.dragTo(board.locator(`section[data-stage="${targetStage}"]`));
  await expect(page.getByText(`Moved to ${targetStage}`)).toBeVisible();
  await expect(board.locator(`section[data-stage="${targetStage}"] li[data-opportunity="${name}"]`)).toBeVisible();
});
