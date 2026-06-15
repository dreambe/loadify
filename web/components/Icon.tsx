"use client";

// Icon is a tiny zero-dependency line-icon set (stroke style matching the
// PulseMark brand glyph) replacing emoji/dingbats for a consistent, crisp
// look across platforms. currentColor inherits the text color.
const paths: Record<string, React.ReactNode> = {
  rerun: <path d="M4 12a8 8 0 1 1 2.3 5.6M4 20v-4h4" />,
  report: (
    <>
      <path d="M6 2h8l4 4v16H6z" />
      <path d="M14 2v4h4M9 13h6M9 17h6M9 9h2" />
    </>
  ),
  download: <path d="M12 3v12m0 0 4-4m-4 4-4-4M5 21h14" />,
  upload: <path d="M12 21V9m0 0 4 4m-4-4-4 4M5 3h14" />,
  stop: (
    <>
      <circle cx="12" cy="12" r="9" />
      <rect x="9" y="9" width="6" height="6" rx="1" />
    </>
  ),
  play: <path d="M7 5v14l11-7z" />,
  sun: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5 19 19M19 5l-1.5 1.5M6.5 17.5 5 19" />
    </>
  ),
  moon: <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />,
  star: <path d="M12 3l2.6 5.3 5.9.9-4.3 4.1 1 5.8-5.2-2.7-5.2 2.7 1-5.8L3.5 9.2l5.9-.9z" />,
  warn: <path d="M12 3 2 20h20L12 3zM12 9v5M12 17.5v.5" />,
  check: <path d="M5 13l4 4L19 7" />,
  x: <path d="M6 6l12 12M18 6 6 18" />,
  chevron: <path d="M6 9l6 6 6-6" />,
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M19.1 4.9 17 7M7 17l-2.1 2.1" />
    </>
  ),
};

export default function Icon({
  name,
  size = 15,
  className,
  style,
}: {
  name: keyof typeof paths | string;
  size?: number;
  className?: string;
  style?: React.CSSProperties;
}) {
  const body = paths[name] ?? null;
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      style={{ flex: "none", verticalAlign: "-2px", ...style }}
      aria-hidden
    >
      {body}
    </svg>
  );
}
