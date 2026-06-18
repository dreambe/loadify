import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";

const API = process.env.E2E_API_URL || "http://localhost:8080";

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
  await page.getByTestId("compare-a").fill(runId);
  await page.getByTestId("compare-b").fill(runId);
  await expect(page.getByText(/p95/i).first()).toBeVisible();
});

test("compare picker dropdown filters, stays in the panel, and selects", async ({ page }) => {
  await page.goto("/compare");
  const a = page.getByTestId("compare-a");
  await a.click();
  // Custom dropdown (not native datalist) must appear as a listbox in the DOM.
  const list = page.locator('ul.combo-list[role="listbox"]').first();
  await expect(list).toBeVisible();
  // It must be contained, not overflowing far past the viewport.
  const box = await list.boundingBox();
  const vw = page.viewportSize()!.width;
  expect(box!.x + box!.width).toBeLessThanOrEqual(vw + 1);
  // Typing filters the options (substring), then a click selects one. Derive the
  // filter term from a real option so the test doesn't depend on seed names.
  const opts = page.locator(".combo-opt");
  if ((await opts.count()) === 0) test.skip(true, "no seeded runs available");
  const term = (await opts.first().innerText()).trim().slice(0, 4);
  await a.fill(term);
  await expect(opts.first()).toBeVisible();
  await opts.first().click();
  // Selection closes the dropdown and populates the field.
  await expect(list).toBeHidden();
  expect((await a.inputValue()).length).toBeGreaterThan(0);
});

test("chart PNG export contains the rendered data, not a blank canvas", async ({ page }) => {
  // Open a finished run (its historical charts have an export button + data).
  await page.goto("/runs");
  const firstRunLink = page.locator('a[href^="/runs/"]').first();
  if ((await firstRunLink.count()) === 0) test.skip(true, "no seeded runs available");
  await firstRunLink.click();
  await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{8,}/);

  const exportBtn = page.getByRole("button", { name: /导出 PNG|Export PNG/ }).first();
  await expect(exportBtn).toBeVisible();

  const [download] = await Promise.all([page.waitForEvent("download"), exportBtn.click()]);
  const file = await download.path();
  expect(file).toBeTruthy();
  const b64 = readFileSync(file!).toString("base64");

  // Decode the PNG and count clearly-colored (non-background) pixels: the data
  // line/area uses bright series colors, the panel bg is near-black. A blank
  // export (the var(--yellow)-not-inlined bug) has almost none.
  const colored = await page.evaluate(async (dataB64) => {
    const img = new Image();
    await new Promise((res, rej) => {
      img.onload = res;
      img.onerror = rej;
      img.src = "data:image/png;base64," + dataB64;
    });
    const c = document.createElement("canvas");
    c.width = img.width;
    c.height = img.height;
    const ctx = c.getContext("2d")!;
    ctx.drawImage(img, 0, 0);
    const { data } = ctx.getImageData(0, 0, c.width, c.height);
    let n = 0;
    for (let i = 0; i < data.length; i += 4) {
      if (data[i] > 150 || data[i + 1] > 120 || data[i + 2] > 150) n++;
    }
    return n;
  }, b64);

  expect(colored, "exported PNG should contain a visible data line/area").toBeGreaterThan(300);
});

test("report (PDF/print) shows chart data with print-contrast colors", async ({ page }) => {
  await page.goto("/runs");
  const firstRunLink = page.locator('a[href^="/runs/"]').first();
  if ((await firstRunLink.count()) === 0) test.skip(true, "no seeded runs available");
  await firstRunLink.click();
  await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{8,}/);
  const runId = page.url().split("/runs/")[1].split(/[?#]/)[0];
  const token = await page.evaluate(() => localStorage.getItem("loadify_token"));
  expect(token).toBeTruthy();

  // The report is the PDF source (browser "print / save as PDF").
  await page.goto(`${API}/api/v1/runs/${runId}/report.html?token=${token}&lang=en`);
  const qps = page.locator("path.spark-qps");
  if ((await qps.count()) === 0) test.skip(true, "run produced no series");
  // Data is present: the sparkline path has a real d attribute.
  expect(((await qps.getAttribute("d")) || "").length).toBeGreaterThan(10);

  // Under print media the line must switch to the dark, high-contrast hue so it
  // doesn't wash out on white paper (the on-screen amber/cyan are near-invisible
  // on white). #0e7490 == rgb(14, 116, 144).
  await page.emulateMedia({ media: "print" });
  const stroke = await qps.evaluate((el) => getComputedStyle(el as Element).stroke);
  expect(stroke.replace(/\s/g, "")).toBe("rgb(14,116,144)");
});

test("share link works with no session: run renders + CSV downloads with % units", async ({ page, browser }) => {
  await page.goto("/runs");
  const link = page.locator('a[href^="/runs/"]').first();
  if ((await link.count()) === 0) test.skip(true, "no seeded runs available");
  await link.click();
  await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{8,}/);
  const runId = page.url().split("/runs/")[1].split(/[?#]/)[0];
  const token = await page.evaluate(() => localStorage.getItem("loadify_token"));

  // Mint a public share token via the API (operator+).
  const mint = await fetch(`${API}/api/v1/runs/${runId}/share`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}` },
  });
  expect(mint.ok).toBeTruthy();
  const { token: share } = (await mint.json()) as { token: string };
  expect(share).toBeTruthy();

  // Open the share link in a FRESH context with NO session — must not bounce to
  // login and must render the run.
  const ctx = await browser.newContext();
  const p = await ctx.newPage();
  await p.goto(`/runs/${runId}?share=${encodeURIComponent(share)}`);
  await expect(p).toHaveURL(new RegExp(`/runs/${runId}`));
  await expect(p.locator(".nav")).toBeVisible();
  await expect(p.getByText(/p95/i).first()).toBeVisible();

  // CSV export must carry the share token (was 401 in share mode before) and use
  // percent units for error rate (matches the on-screen %).
  const csvHref = await p.locator('a[download][href*="export.csv"]').first().getAttribute("href");
  expect(csvHref, "CSV link should exist").toBeTruthy();
  expect(csvHref).toContain("share=");
  const csv = await fetch(csvHref!);
  expect(csv.status).toBe(200);
  const header = (await csv.text()).split("\n")[0];
  expect(header).toContain("error_rate_pct");
  await ctx.close();
});

test("no NaN/Infinity rendered on run and compare pages", async ({ page }) => {
  await page.goto("/runs");
  const link = page.locator('a[href^="/runs/"]').first();
  if ((await link.count()) > 0) {
    await link.click();
    await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{8,}/);
    await expect(page.locator(".nav")).toBeVisible();
    const body = await page.locator("body").innerText();
    expect(body, "run page must not show NaN/Infinity").not.toMatch(/\bNaN\b|Infinity/);
  }
  await page.goto("/compare");
  const cmp = await page.locator("body").innerText();
  expect(cmp, "compare page must not show NaN/Infinity").not.toMatch(/\bNaN\b|Infinity/);
});

test("light theme: pages mount and the spinner stays visible", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("loadify_theme", "light"));
  for (const path of ["/", "/runs", "/users"]) {
    await page.goto(path);
    await expect(page.locator(".nav")).toBeVisible();
    await expect.poll(() => page.evaluate(() => document.documentElement.dataset.theme)).toBe("light");
  }
  // The spinner's ring (the color-mix border) must resolve to a visible color on
  // the light background, not transparent.
  const ring = await page.evaluate(() => {
    const el = document.createElement("div");
    el.className = "spinner";
    document.body.appendChild(el);
    const c = getComputedStyle(el).borderLeftColor;
    el.remove();
    return c;
  });
  expect(ring).not.toBe("rgba(0, 0, 0, 0)");
  expect(ring).not.toBe("transparent");
});
