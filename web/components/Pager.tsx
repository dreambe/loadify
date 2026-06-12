"use client";

import { useMemo, useState } from "react";

// usePager slices a client-side list into pages and resets when it shrinks.
export function usePager<T>(items: T[], pageSize = 10) {
  const [page, setPage] = useState(0);
  const pages = Math.max(1, Math.ceil(items.length / pageSize));
  const cur = Math.min(page, pages - 1);
  const slice = useMemo(
    () => items.slice(cur * pageSize, (cur + 1) * pageSize),
    [items, cur, pageSize]
  );
  return { slice, page: cur, pages, setPage, total: items.length };
}

export function Pager({
  page,
  pages,
  total,
  onPage,
}: {
  page: number;
  pages: number;
  total: number;
  onPage: (p: number) => void;
}) {
  if (pages <= 1) return null;
  return (
    <div className="pager">
      <span>{total}</span>
      <button className="secondary" disabled={page === 0} onClick={() => onPage(page - 1)}>
        ‹
      </button>
      <span>
        {page + 1} / {pages}
      </span>
      <button
        className="secondary"
        disabled={page >= pages - 1}
        onClick={() => onPage(page + 1)}
      >
        ›
      </button>
    </div>
  );
}
