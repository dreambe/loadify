"use client";

import Link from "next/link";
import { clearSession, getUser } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";

export default function Nav() {
  const user = getUser();
  const { t, lang, setLang } = useI18n();
  return (
    <nav className="nav">
      <Link className="brand" href="/runs">
        loadify
      </Link>
      <Link href="/runs">{t("nav.runs")}</Link>
      <Link href="/tests">{t("nav.tests")}</Link>
      <Link href="/compare">{t("nav.compare")}</Link>
      <Link href="/workers">{t("nav.workers")}</Link>
      {user?.role === "admin" && <Link href="/users">{t("nav.users")}</Link>}
      <span className="spacer" />
      <button
        className="secondary"
        onClick={() => setLang(lang === "zh" ? "en" : "zh")}
        title="切换语言 / Switch language"
      >
        {lang === "zh" ? "EN" : "中文"}
      </button>
      {user && (
        <span className="me">
          {user.name || user.email} · {user.role}
        </span>
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
