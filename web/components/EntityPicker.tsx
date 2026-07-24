"use client";

import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useI18n } from "@/lib/i18n";

// EntityPicker is the single searchable combobox used wherever the user picks a
// record by typing (tests on the runs page, runs on the compare page). It is a
// CONTROLLED custom dropdown (not a native <datalist>, which the browser renders
// unstyled and lets overflow/detach from the input). The contract:
//   - label(item): the human-readable text shown in the dropdown and field.
//   - keys(item):  every string the typed/pasted value may match (id, short id,
//                  name, …). Matching is substring against label()+keys, and
//                  selection resolves a value produced elsewhere (a copied ID).
//   - accept(raw): optional escape hatch — resolve a value not in `items`
//                  (e.g. a valid ID outside the loaded window) to an id string.
export interface EntityPickerProps<T> {
  items: T[];
  value: string;
  onChange: (id: string) => void;
  idOf: (item: T) => string;
  label: (item: T) => string;
  keys?: (item: T) => string[];
  accept?: (raw: string) => string | undefined;
  placeholder?: string;
  listId: string;
  testId?: string;
  className?: string;
  style?: React.CSSProperties;
}

export default function EntityPicker<T>({
  items,
  value,
  onChange,
  idOf,
  label,
  keys,
  accept,
  placeholder,
  listId,
  testId,
  className,
  style,
}: EntityPickerProps<T>) {
  const { t } = useI18n();
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const reactId = useId();
  const popId = listId || reactId;

  // Keep the field in sync with the selected value: show its label, or the raw
  // id when it's a valid selection outside the loaded list (don't blank it).
  useEffect(() => {
    const item = items.find((x) => idOf(x) === value);
    setText(item ? label(item) : value);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, items.length]);

  // Close when clicking outside.
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  const q = text.trim().toLowerCase();
  const filtered = useMemo(() => {
    if (!q) return items;
    // When the field still shows the selected item's label, treat it as
    // "browsing" (show everything) rather than filtering to that one row.
    const sel = items.find((x) => idOf(x) === value);
    if (sel && label(sel).toLowerCase() === q) return items;
    return items.filter((x) =>
      (label(x) + " " + (keys?.(x) ?? []).join(" ")).toLowerCase().includes(q)
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, items, value]);

  // Reset the highlight when the visible set changes.
  useEffect(() => setActive(0), [q, open]);

  const resolve = (raw: string): string => {
    const v = raw.trim();
    if (!v) return "";
    const lower = v.toLowerCase();
    const hit = items.find(
      (x) => label(x) === v || (keys?.(x) ?? []).some((k) => k.toLowerCase() === lower)
    );
    if (hit) return idOf(hit);
    return accept?.(v) ?? "";
  };

  function choose(item: T) {
    onChange(idOf(item));
    setText(label(item));
    setOpen(false);
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      if (!open) setOpen(true);
      else setActive((a) => Math.min(filtered.length - 1, a + 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(0, a - 1));
    } else if (e.key === "Enter") {
      if (open && filtered[active]) {
        e.preventDefault();
        choose(filtered[active]);
      }
    } else if (e.key === "Escape") {
      setOpen(false);
    }
  }

  return (
    <div className={"combo" + (className ? " " + className : "")} ref={rootRef} style={{ position: "relative", ...style }}>
      <input
        data-testid={testId}
        role="combobox"
        aria-expanded={open}
        aria-controls={popId}
        autoComplete="off"
        value={text}
        placeholder={placeholder}
        style={{ width: "100%", paddingRight: 28 }}
        onChange={(e) => {
          setText(e.target.value);
          setOpen(true);
          onChange(resolve(e.target.value));
        }}
        onFocus={() => setOpen(true)}
        // Reopen on click even when already focused — otherwise, after picking an
        // option (which closes the list) you'd have to blur and refocus to pick
        // another; onFocus won't re-fire while focused.
        onClick={() => setOpen(true)}
        onKeyDown={onKeyDown}
      />
      <span className="combo-caret" aria-hidden onMouseDown={(e) => { e.preventDefault(); setOpen((o) => !o); }}>
        <svg width="10" height="6" viewBox="0 0 10 6">
          <path d="M1 1l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </span>
      {open && (
        <ul className="combo-list" id={popId} role="listbox">
          {filtered.length === 0 ? (
            <li className="combo-empty">{t("common.noMatch")}</li>
          ) : (
            filtered.slice(0, 100).map((x, i) => (
              <li
                key={idOf(x)}
                role="option"
                aria-selected={idOf(x) === value}
                className={"combo-opt" + (i === active ? " active" : "")}
                // mousedown (not click) so it fires before the input blur closes.
                onMouseDown={(e) => {
                  e.preventDefault();
                  choose(x);
                }}
                onMouseEnter={() => setActive(i)}
              >
                {label(x)}
              </li>
            ))
          )}
        </ul>
      )}
    </div>
  );
}
