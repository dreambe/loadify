import { mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

// global-setup waits for the stack, logs in as the bootstrap admin, seeds two
// finished runs (so the compare page has A/B), and writes a Playwright
// storageState that carries the session via localStorage (the app stores its
// JWT there). Seeding through the public API keeps the fixtures realistic.
const API = process.env.E2E_API_URL || "http://localhost:8080";
const BASE = process.env.E2E_BASE_URL || "http://localhost:3000";
const EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@loadify.local";
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || "admin12345";
// Where seeded runs send traffic — must be reachable from the worker container.
const TARGET = process.env.E2E_TARGET_URL || "http://echo-target:8088/";
const AUTH_FILE = "e2e/.auth/admin.json";

async function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

async function waitForLogin(): Promise<string> {
  // apisrv may still be starting; retry login for up to ~2 minutes.
  for (let i = 0; i < 60; i++) {
    try {
      const res = await fetch(`${API}/api/v1/auth/login`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
      });
      if (res.ok) {
        const j = (await res.json()) as { token: string };
        return j.token;
      }
    } catch {
      /* not up yet */
    }
    await sleep(2000);
  }
  throw new Error("apisrv did not become ready / admin login failed");
}

async function seedRun(token: string, name: string): Promise<string> {
  const h = { "Content-Type": "application/json", Authorization: `Bearer ${token}` };
  // A tiny HTTP test against the echo target with a short ramp so it finishes
  // quickly.
  const test = await fetch(`${API}/api/v1/tests`, {
    method: "POST",
    headers: h,
    body: JSON.stringify({
      name: `e2e-${name}`,
      protocol: "http",
      plan: { protocol: "http", http: { url: TARGET } },
      ramp: [{ duration_ms: 3000, target_vus: 3 }],
    }),
  });
  if (!test.ok) throw new Error(`seed test failed: ${test.status} ${await test.text()}`);
  const { id: testId } = (await test.json()) as { id: string };

  const run = await fetch(`${API}/api/v1/runs`, {
    method: "POST",
    headers: h,
    body: JSON.stringify({ test_id: testId, desired_workers: 1, name: `run-${name}` }),
  });
  if (!run.ok) throw new Error(`seed run failed: ${run.status} ${await run.text()}`);
  const { run_id } = (await run.json()) as { run_id: string };

  // Wait until it leaves the active states so the compare page has final metrics.
  for (let i = 0; i < 40; i++) {
    const r = await fetch(`${API}/api/v1/runs/${run_id}`, { headers: h });
    if (r.ok) {
      const j = (await r.json()) as { status: string };
      if (!["pending", "queued", "running"].includes(j.status)) break;
    }
    await sleep(2000);
  }
  return run_id;
}

export default async function globalSetup() {
  const token = await waitForLogin();
  const me = await fetch(`${API}/api/v1/auth/me`, { headers: { Authorization: `Bearer ${token}` } });
  const user = me.ok ? await me.json() : { id: "admin", email: EMAIL, name: "admin", role: "admin" };

  // Seed two runs for the compare flow (best-effort: if workers aren't up the
  // run-detail/compare tests will still exercise the UI with whatever exists).
  try {
    await seedRun(token, "a");
    await seedRun(token, "b");
  } catch (e) {
    console.warn("e2e seed warning:", (e as Error).message);
  }

  const origin = new URL(BASE).origin;
  const state = {
    cookies: [],
    origins: [
      {
        origin,
        localStorage: [
          { name: "loadify_token", value: token },
          { name: "loadify_user", value: JSON.stringify(user) },
        ],
      },
    ],
  };
  mkdirSync(dirname(AUTH_FILE), { recursive: true });
  writeFileSync(AUTH_FILE, JSON.stringify(state, null, 2));
}
