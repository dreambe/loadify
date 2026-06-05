"use client";

import { Suspense, useEffect, useState } from "react";
import { api } from "@/lib/api";
import { setSession, setToken } from "@/lib/auth";

function LoginInner() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  // Feishu callback redirects back with the token in the URL fragment.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const hash = window.location.hash;
    const m = hash.match(/token=([^&]+)/);
    if (m) {
      setToken(decodeURIComponent(m[1]));
      api
        .me()
        .then((u) => {
          setSession(window.localStorage.getItem("loadify_token") || "", u);
          window.location.href = "/runs";
        })
        .catch(() => setErr("feishu login failed"));
    }
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const res = await api.login(email, password);
      setSession(res.token, res.user);
      window.location.href = "/runs";
    } catch (e: any) {
      setErr(e.message || "login failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="container" style={{ maxWidth: 380 }}>
      <h1>Sign in to loadify</h1>
      <form className="panel" onSubmit={submit}>
        <label>Email</label>
        <input value={email} onChange={(e) => setEmail(e.target.value)} type="email" required />
        <label>Password</label>
        <input
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          type="password"
          required
        />
        {err && <div className="error">{err}</div>}
        <div style={{ marginTop: 16 }}>
          <button type="submit" disabled={busy}>
            {busy ? "Signing in…" : "Sign in"}
          </button>
        </div>
      </form>
      <p className="muted" style={{ textAlign: "center" }}>
        <a href={api.feishuLoginURL()}>Sign in with Feishu</a>
      </p>
    </div>
  );
}

export default function LoginPage() {
  return (
    <Suspense fallback={null}>
      <LoginInner />
    </Suspense>
  );
}
