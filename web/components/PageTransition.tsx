"use client";

import { usePathname } from "next/navigation";

// PageTransition re-keys its content on route change so the CSS `page-enter`
// animation replays — a subtle fade/rise between pages. Zero dependencies; the
// keyframes honor prefers-reduced-motion via the global reduced-motion rule.
export default function PageTransition({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  return (
    <div key={pathname} className="page-enter" style={{ minHeight: "calc(100vh - 90px)" }}>
      {children}
    </div>
  );
}
