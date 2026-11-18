import { expect, type Page } from "@playwright/test";

/** Demo login (dev mode only): pick a seeded persona on /login. */
export async function login(page: Page, persona: string) {
  await page.goto("/login");
  await page.locator(`button[data-persona="${persona}"]`).click();
  await expect(page.getByTestId("user-role")).toBeVisible();
}

export const unique = (p: string) => `${p}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
