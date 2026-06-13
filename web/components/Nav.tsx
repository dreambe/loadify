"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { clearSession, getToken, getUser } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
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

export default function Nav() {
  const pathname = usePathname();
  const { t, lang, setLang } = useI18n();
  const [theme, toggleTheme] = useTheme();
  // Start from the cached session, then refresh once so the avatar appears
  // for sessions created before avatars existed.
  const [user, setUser] = useState<User | null>(null);
  useEffect(() => {
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
  }, []);

  const item = (href: string, label: string) => (
    <Link href={href} className={pathname?.startsWith(href) ? "active" : undefined}>
      {label}
    </Link>
  );

  return (
    <nav className="nav">
      <Link className="brand" href="/runs">
        <PulseMark />
        Loadify
      </Link>
      {item("/runs", t("nav.runs"))}
      {item("/tests", t("nav.tests"))}
      {item("/environments", t("nav.environments"))}
      {item("/compare", t("nav.compare"))}
      {item("/workers", t("nav.workers"))}
      {user?.role === "admin" && item("/users", t("nav.users"))}
      <span className="spacer" />
      <button
        className="secondary"
        onClick={toggleTheme}
        title={theme === "dark" ? t("nav.themeLight") : t("nav.themeDark")}
      >
        {theme === "dark" ? "☀" : "☾"}
      </button>
      <button
        className="secondary"
        onClick={() => setLang(lang === "zh" ? "en" : "zh")}
        title="切换语言 / Switch language"
      >
        {lang === "zh" ? "EN" : "中文"}
      </button>
      {user && (
        <Link
          href="/users"
          className="nav-avatar"
          title={`${user.name || user.email} · ${user.role}`}
        >
          {user.avatar_url ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img className="avatar sm" src={user.avatar_url} alt={user.name || user.email} />
          ) : (
            <span className="avatar sm fallback">
              {(user.name || user.email || "?").trim().charAt(0).toUpperCase()}
            </span>
          )}
        </Link>
      )}
      <button
        className="secondary"
        onClick={() => {
          clearSession();
          window.location.href = "/login";
        }}
      >
        {t("nav.signout")}
      </button>
    </nav>
  );
}
