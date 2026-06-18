import { defineConfig, devices } from "@playwright/test";

// Smoke E2E against a running stack (docker compose). These tests exist to catch
// "produce in one place, consume in another" regressions — copy an ID here,
// paste it there; generate a token here, read it back — the class of bug that
// unit tests and tsc miss. Point them at a deployment with:
//   E2E_BASE_URL   web origin   (default http://localhost:3000)
//   E2E_API_URL    api origin   (default http://localhost:8080)
//   E2E_ADMIN_EMAIL / E2E_ADMIN_PASSWORD  bootstrap admin
//   E2E_TARGET_URL the URL seeded runs hit (reachable from the worker)
const baseURL = process.env.E2E_BASE_URL || "http://localhost:3000";

export default defineConfig({
  testDir: "./e2e",
  globalSetup: "./e2e/global-setup.ts",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["github"], ["list"]] : "list",
  use: {
    baseURL,
    storageState: "e2e/.auth/admin.json",
    permissions: ["clipboard-read", "clipboard-write"],
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
