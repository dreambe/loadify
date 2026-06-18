"use client";

// EmptyState is the standard "nothing here yet" placeholder: a faint glyph, a
// title and an optional hint, optionally with an action.
export default function EmptyState({
  title,
  hint,
  action,
}: {
  title: string;
  hint?: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="empty">
      <svg className="glyph" width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
        <polyline points="2,15 7,15 10,7 14,19 17,13 22,13" />
      </svg>
      <div className="title">{title}</div>
      {hint && <div style={{ fontSize: 13 }}>{hint}</div>}
      {action}
    </div>
  );
}
