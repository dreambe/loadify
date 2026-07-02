"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";
import { useI18n } from "@/lib/i18n";

// Route segment → i18n key for the page title. The browser tab and history
// entry then read "<page> · Loadify" instead of a uniform "Loadify", so open
// tabs and back-button history are distinguishable.
const TITLE_KEYS: Record<string, string> = {
  "": "nav.dashboard",
  runs: "nav.runs",
  tests: "nav.tests",
  workers: "nav.workers",
  compare: "nav.compare",
  schedules: "nav.schedules",
  environments: "nav.environments",
  users: "nav.users",
};

// DocumentTitle keeps document.title in sync with the current route and locale.
// Rendered once inside the locale provider; renders nothing.
export default function DocumentTitle() {
  const { t } = useI18n();
  const pathname = usePathname();

  useEffect(() => {
    const seg = (pathname || "/").split("/")[1] ?? "";
    const key = TITLE_KEYS[seg];
    document.title = key ? `${t(key)} · Loadify` : "Loadify";
  }, [pathname, t]);

  return null;
}
