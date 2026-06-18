"use client";

import { Suspense, useEffect, useState } from "react";
import { api } from "@/lib/api";
import { setSession, setToken } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import { PulseMark } from "@/components/Nav";

function LoginInner() {
  const { t, lang, setLang } = useI18n();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  // null = unknown (probing the server); the Feishu entry only renders when
  // the server reports it configured, so users never hit a dead link.
  const [feishuEnabled, setFeishuEnabled] = useState<boolean | null>(null);

  useEffect(() => {
    api
      .authConfig()
      .then((c) => setFeishuEnabled(c.feishu_enabled))
      .catch(() => setFeishuEnabled(false));
  }, []);

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
        .catch(() => setErr(t("login.feishuFailed")));
    }
  }, [t]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const res = await api.login(email, password);
      setSession(res.token, res.user);
      window.location.href = "/runs";
    } catch (e: any) {
      setErr(e.message || t("login.failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="login-shell">
      <div className="login-card">
        <div style={{ textAlign: "right", marginBottom: 8 }}>
          <button className="secondary" onClick={() => setLang(lang === "zh" ? "en" : "zh")}>
            {lang === "zh" ? "EN" : "中文"}
          </button>
        </div>
        <div className="login-mark">
          <PulseMark size={28} />
          Loadify
        </div>
        <p className="muted" style={{ textAlign: "center", marginTop: -12, marginBottom: 20 }}>
          {t("login.tagline")}
        </p>
        <form className="panel" onSubmit={submit}>
          <label>{t("login.email")}</label>
          <input
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            type="email"
            required
            style={{ width: "100%" }}
          />
          <label>{t("login.password")}</label>
          <input
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            type="password"
            required
            style={{ width: "100%" }}
          />
          {err && <div className="error">{err}</div>}
          <div style={{ marginTop: 16 }}>
            <button type="submit" disabled={busy} style={{ width: "100%" }}>
              {busy ? t("login.signingin") : t("login.signin")}
            </button>
          </div>
        </form>
        <p className="muted" style={{ textAlign: "center" }}>
          {feishuEnabled === true && <a href={api.feishuLoginURL()}>{t("login.feishu")}</a>}
          {feishuEnabled === false && (
            <span style={{ fontSize: 12.5 }}>{t("login.feishuDisabled")}</span>
          )}
        </p>
      </div>
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
