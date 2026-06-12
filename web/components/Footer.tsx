"use client";

import { useI18n } from "@/lib/i18n";
import { PulseMark } from "./Nav";

export default function Footer() {
  const { t } = useI18n();
  return (
    <footer className="footer">
      <div className="footer-inner">
        <span className="brand">
          <PulseMark size={16} />
          Loadify
        </span>
        <span>{t("footer.tagline")}</span>
        <span className="spacer" />
        <a href="https://github.com/dreambe/loadify" target="_blank" rel="noreferrer">
          GitHub
        </a>
        <a href="/openapi.yaml" target="_blank" rel="noreferrer">
          OpenAPI
        </a>
        <span>© {new Date().getFullYear()} Loadify</span>
      </div>
    </footer>
  );
}
