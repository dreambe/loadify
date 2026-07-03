"use client";

import { useMemo, useRef, useState } from "react";
import { useI18n } from "@/lib/i18n";

// Built-in template functions offered by the autocomplete. insert is the text
// placed between the braces — argument-taking functions insert a working
// example so the user edits values instead of recalling signatures.
const FUNCTIONS = [
  { name: "uuid", insert: "uuid" },
  { name: "mobile", insert: "mobile" },
  { name: "email", insert: "email" },
  { name: "randomString", insert: "randomString(8)" },
  { name: "randomDigits", insert: "randomDigits(6)" },
  { name: "randomInt", insert: "randomInt(1,100)" },
  { name: "randomFloat", insert: "randomFloat(0,1)" },
  { name: "randomHex", insert: "randomHex(16)" },
  { name: "pick", insert: "pick(A|B|C)" },
  { name: "seq", insert: "seq" },
  { name: "ipv4", insert: "ipv4" },
  { name: "timestamp", insert: "timestamp" },
  { name: "now", insert: "now" },
  { name: "random", insert: "random" },
];

interface Suggestion {
  label: string;
  insert: string;
  desc: string;
  kind: "col" | "fn";
}

// TemplateInput is an input/textarea that autocompletes {{...}} template
// tokens: the user's dataset columns first, then the built-in generator
// functions — recognition instead of recall. Typing "{{" (or "{{ran") opens
// the list; ↑/↓ navigate, Enter/Tab insert (closing braces added), Esc closes.
export default function TemplateInput({
  value,
  onChange,
  columns,
  multiline,
  rows,
  placeholder,
  style,
  required,
}: {
  value: string;
  onChange: (v: string) => void;
  columns?: string[];
  multiline?: boolean;
  rows?: number;
  placeholder?: string;
  style?: React.CSSProperties;
  required?: boolean;
}) {
  const { t } = useI18n();
  const ref = useRef<HTMLInputElement | HTMLTextAreaElement | null>(null);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);

  const items = useMemo<Suggestion[]>(() => {
    const q = query.toLowerCase();
    const cols = (columns ?? [])
      .filter((c) => c.toLowerCase().startsWith(q))
      .map((c): Suggestion => ({ label: c, insert: c, desc: t("tpl.colDesc"), kind: "col" }));
    const fns = FUNCTIONS.filter((f) => f.name.toLowerCase().startsWith(q)).map(
      (f): Suggestion => ({ label: f.insert, insert: f.insert, desc: t("fn." + f.name), kind: "fn" })
    );
    return [...cols, ...fns].slice(0, 9);
  }, [query, columns, t]);

  // detect reads the caret from the live element (never the possibly-stale
  // prop) and opens the list when an unclosed {{token sits at the caret.
  const detect = (el: HTMLInputElement | HTMLTextAreaElement) => {
    const caret = el.selectionStart ?? 0;
    const m = /\{\{\s*([\w.]*)$/.exec(el.value.slice(0, caret));
    if (m) {
      setQuery(m[1]);
      setActive(0);
      setOpen(true);
    } else {
      setOpen(false);
    }
  };

  const insert = (s: Suggestion) => {
    const el = ref.current;
    if (!el) return;
    const caret = el.selectionStart ?? 0;
    const before = el.value.slice(0, caret);
    const m = /\{\{\s*([\w.]*)$/.exec(before);
    if (!m) {
      setOpen(false);
      return;
    }
    const start = caret - m[1].length;
    const after = el.value.slice(caret);
    const closer = after.startsWith("}}") ? "" : "}}";
    onChange(el.value.slice(0, start) + s.insert + closer + after);
    setOpen(false);
    const pos = start + s.insert.length + 2;
    requestAnimationFrame(() => {
      el.setSelectionRange(pos, pos);
      el.focus();
    });
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!open || items.length === 0) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => (a + 1) % items.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => (a - 1 + items.length) % items.length);
    } else if (e.key === "Enter" || e.key === "Tab") {
      e.preventDefault();
      insert(items[active]);
    } else if (e.key === "Escape") {
      e.stopPropagation();
      setOpen(false);
    }
  };

  const shared = {
    value,
    placeholder,
    required,
    style: { width: "100%" } as React.CSSProperties,
    onKeyDown,
    onBlur: () => setOpen(false),
    onClick: (e: React.MouseEvent<HTMLInputElement | HTMLTextAreaElement>) => detect(e.currentTarget),
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
      onChange(e.target.value);
      detect(e.target);
    },
  };

  return (
    <div style={{ position: "relative", ...style }}>
      {multiline ? (
        <textarea ref={(el) => void (ref.current = el)} rows={rows ?? 4} {...shared} />
      ) : (
        <input ref={(el) => void (ref.current = el)} {...shared} />
      )}
      {open && items.length > 0 && (
        <div className="tpl-suggest" role="listbox">
          {items.map((s, i) => (
            <div
              key={s.kind + s.label}
              role="option"
              aria-selected={i === active}
              className={"tpl-item" + (i === active ? " on" : "")}
              // mousedown (not click) so the input's blur doesn't close the
              // list before the selection lands.
              onMouseDown={(e) => {
                e.preventDefault();
                insert(s);
              }}
              onMouseEnter={() => setActive(i)}
            >
              <span className="tpl-kind">{s.kind === "col" ? t("tpl.kindCol") : t("tpl.kindFn")}</span>
              <span className="tpl-token">{"{{" + s.label + "}}"}</span>
              <span className="tpl-desc">{s.desc}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
