"use client";

import type { ReactNode } from "react";

// SortableTh is a table header cell that sorts its column. The interactive part
// is a real <button> (keyboard-focusable, Enter/Space activate it) and the cell
// carries aria-sort, so screen readers announce the sort state — a bare
// <th onClick> does neither. The arrow reserves its space to avoid layout shift.
export default function SortableTh({
  label,
  active,
  dir,
  onToggle,
}: {
  label: ReactNode;
  active: boolean;
  dir: "asc" | "desc";
  onToggle: () => void;
}) {
  return (
    <th aria-sort={active ? (dir === "asc" ? "ascending" : "descending") : "none"}>
      <button type="button" className="th-sort" onClick={onToggle}>
        {label}
        <span className="th-sort-mark" aria-hidden>
          {active ? (dir === "desc" ? "▼" : "▲") : ""}
        </span>
      </button>
    </th>
  );
}
