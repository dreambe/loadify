import { test, expect } from "@playwright/test";

// These smoke tests target the "produce here, consume there" seams — the bug
// class that unit tests and tsc cannot catch (copy an ID / token in one place,
// use it in another). Each asserts the loop closes, not just that a page loads.

test("every primary page renders without error", async ({ page }) => {
  for (const path of ["/", "/runs", "/tests", "/compare", "/environments", "/schedules", "/workers", "/users"]) {
    const resp = await page.goto(path);
    expect(resp?.status(), `GET ${path}`).toBeLessThan(400);
    // The app shell (nav) must mount — a blank/crashed page has no nav.
    await expect(page.locator(".nav")).toBeVisible();
  }
});

test("persistent API token is shown, copyable in full, and resettable", async ({ page }) => {
  await page.goto("/users");
  const panel = page.getByTestId("api-token-panel");
  const input = page.getByTestId("api-token-input");
  await expect(panel).toBeVisible();

  // Reveal so the field holds the full token, then assert it's a complete value.
  await panel.getByRole("button", { name: /显示|Show/ }).click();
  const before = await input.inputValue();
  expect(before).toMatch(/^lfy_[0-9a-f]{8,}/);

  // Reset issues a different token (the produce side); confirm dialog -> new value.
  await panel.getByRole("button", { name: /重置|Reset/ }).click();
  await page.getByRole("button", { name: /重置|Reset|确定|Confirm/ }).last().click();
  await expect.poll(async () => input.inputValue()).not.toBe(before);
});

test("run ID copied on the detail page resolves in the compare picker", async ({ page }) => {
  // Find a finished run to drive the loop.
  const runsResp = await page.goto("/runs");
  expect(runsResp?.status()).toBeLessThan(400);
  // Open the most recent run from the list.
  const firstRunLink = page.locator('a[href^="/runs/"]').first();
  if ((await firstRunLink.count()) === 0) test.skip(true, "no seeded runs available");
  await firstRunLink.click();
  await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{8,}/);
  const runId = page.url().split("/runs/")[1].split(/[?#]/)[0];
  expect(runId.length).toBeGreaterThan(8);

  // The detail page must surface the ID (the chip) so it's referenceable at all.
  await expect(page.locator(".id-chip")).toBeVisible();

  // Consume side: paste the FULL id into both compare pickers; the comparison
  // table appears only when both sides resolved — this is exactly the short/long
  // ID bug that shipped before.
  await page.goto("/compare");
  await page.locator('input[list="compare-a"]').fill(runId);
  await page.locator('input[list="compare-b"]').fill(runId);
  await expect(page.getByText(/p95/i).first()).toBeVisible();
});
