"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { clearSession, getToken, getUser } from "@/lib/auth";
import { useI18n, roleLabel } from "@/lib/i18n";
import Icon from "./Icon";
import type { User } from "@/lib/types";

const THEME_KEY = "loadify_theme";

// useTheme persists a light/dark preference on <html data-theme>; dark is the
// default and is encoded as the absence of the attribute.
export function useTheme(): ["dark" | "light", () => void] {
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  useEffect(() => {
    const stored = window.localStorage.getItem(THEME_KEY);
    if (stored === "light") {
      setTheme("light");
      document.documentElement.dataset.theme = "light";
    }
  }, []);
  const toggle = () => {
    const next = theme === "dark" ? "light" : "dark";
    setTheme(next);
    window.localStorage.setItem(THEME_KEY, next);
    if (next === "light") document.documentElement.dataset.theme = "light";
    else delete document.documentElement.dataset.theme;
  };
  return [theme, toggle];
}

// PulseMark is the brand glyph: a load-curve heartbeat.
export function PulseMark({ size = 20 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <polyline points="1.5,13 6.5,13 9.5,4.5 14.5,19.5 17.5,13 22.5,13" />
    </svg>
  );
}

export default function Nav({ brandOnly }: { brandOnly?: boolean }) {
  const pathname = usePathname();
  const { t } = useI18n();
  // Start from the cached session, then refresh once so the avatar appears
  // for sessions created before avatars existed.
  const [user, setUser] = useState<User | null>(null);
  useEffect(() => {
    if (brandOnly) return;
    setUser(getUser());
    if (getToken()) {
      api
        .me()
        .then((u) => {
          setUser(u);
          window.localStorage.setItem("loadify_user", JSON.stringify(u));
        })
        .catch(() => {});
    }
  }, [brandOnly]);

  // Share-link (anonymous) chrome: just the brand — every tab and the account
  // menu would dead-end at the login page for a viewer with no session.
  if (brandOnly) {
    return (
      <nav className="nav">
        <span className="brand">
          <PulseMark size={24} />
          Loadify
        </span>
      </nav>
    );
  }

  const item = (href: string, label: string) => {
    // "/" must match exactly (it's a prefix of everything); others match by prefix.
    const active = href === "/" ? pathname === "/" : pathname?.startsWith(href);
    return (
      <Link href={href} className={active ? "active" : undefined}>
        {label}
      </Link>
    );
  };

  return (
    <nav className="nav">
      {/* 3-column grid: brand left, tabs truly centered in the viewport,
          account menu right — independent of the brand/account widths. */}
      <Link className="brand" href="/">
        <PulseMark size={24} />
        Loadify
      </Link>
      <div className="nav-tabs">
        {item("/", t("nav.dashboard"))}
        {/* Primary loop: run → author → analyze. */}
        {item("/runs", t("nav.runs"))}
        {item("/tests", t("nav.tests"))}
        {item("/compare", t("nav.compare"))}
        <span className="nav-sep" aria-hidden />
        {/* Configuration & automation. */}
        {item("/environments", t("nav.environments"))}
        {item("/schedules", t("nav.schedules"))}
        <span className="nav-sep" aria-hidden />
        {/* Ops. */}
        {item("/workers", t("nav.workers"))}
      </div>
      <AccountMenu user={user} />
    </nav>
  );
}

// AccountMenu collapses identity, preferences (theme/language) and sign-out into
// the avatar — the canonical "account menu" pattern. Navigation and sign-out
// close the menu; preference toggles keep it open so the change is visible.
function AccountMenu({ user }: { user: User | null }) {
  const { t, lang, setLang } = useI18n();
  const [theme, toggleTheme] = useTheme();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (!user) return null;
  const initial = (user.name || user.email || "?").trim().charAt(0).toUpperCase();
  const avatar = (cls: string) =>
    user.avatar_url ? (
      // eslint-disable-next-line @next/next/no-img-element
      <img className={`avatar ${cls}`} src={user.avatar_url} alt={user.name || user.email} />
    ) : (
      <span className={`avatar ${cls} fallback`}>{initial}</span>
    );

  return (
    <div className="user-menu" ref={ref}>
      <button
        className="user-menu-trigger"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={user.name || user.email}
        onClick={() => setOpen((v) => !v)}
      >
        {avatar("sm")}
        <Icon name="chevron" size={13} className={"nav-caret" + (open ? " up" : "")} />
      </button>
      {open && (
        <div className="menu" role="menu">
          <div className="menu-head">
            {avatar("")}
            <div style={{ minWidth: 0 }}>
              <div className="menu-name">{user.name || user.email}</div>
              {user.name && <div className="menu-mail">{user.email}</div>}
              <span className="badge completed" style={{ marginTop: 4 }}>
                {roleLabel(t, user.role)}
              </span>
            </div>
          </div>

          {/* Everyone can reach /users for their own account (password, API
              token, webhooks); admins additionally get user management there. */}
          <div className="menu-sep" />
          <Link role="menuitem" className="menu-item" href="/users" onClick={() => setOpen(false)}>
            {t("nav.users")}
          </Link>

          <div className="menu-sep" />
          <div className="menu-row">
            <span>{t("nav.theme")}</span>
            <div className="seg" role="group" aria-label={t("nav.theme")}>
              <button
                className={"seg-btn" + (theme === "light" ? " on" : "")}
                aria-pressed={theme === "light"}
                aria-label={t("nav.themeLightName")}
                onClick={() => theme !== "light" && toggleTheme()}
              >
                <Icon name="sun" size={14} />
              </button>
              <button
                className={"seg-btn" + (theme === "dark" ? " on" : "")}
                aria-pressed={theme === "dark"}
                aria-label={t("nav.themeDarkName")}
                onClick={() => theme !== "dark" && toggleTheme()}
              >
                <Icon name="moon" size={14} />
              </button>
            </div>
          </div>
          <div className="menu-row">
            <span>{t("nav.language")}</span>
            <div className="seg" role="group" aria-label={t("nav.language")}>
              <button
                className={"seg-btn" + (lang === "zh" ? " on" : "")}
                aria-pressed={lang === "zh"}
                onClick={() => setLang("zh")}
              >
                中
              </button>
              <button
                className={"seg-btn" + (lang === "en" ? " on" : "")}
                aria-pressed={lang === "en"}
                onClick={() => setLang("en")}
              >
                EN
              </button>
            </div>
          </div>

          <div className="menu-sep" />
          <button
            role="menuitem"
            className="menu-item danger"
            onClick={() => {
              clearSession();
              window.location.href = "/login";
            }}
          >
            {t("nav.signout")}
          </button>
        </div>
      )}
    </div>
  );
}
