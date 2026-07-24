"use client";

import { useEffect, useId, useRef, useState } from "react";

export interface SelectOption {
  value: string;
  label: string;
}

// Select is the app's single styled single-choice dropdown: a trigger that
// looks like a themed input plus a custom popup (the same .combo-list surface as
// EntityPicker), so every dropdown matches the dark theme instead of falling
// back to the browser/OS-native <select> popup. Use EntityPicker instead when
// the user needs to search a long list of records.
export default function Select({
  value,
  onChange,
  options,
  placeholder,
  disabled,
  style,
  className,
  ariaLabel,
}: {
  value: string;
  onChange: (v: string) => void;
  options: SelectOption[];
  placeholder?: string;
  disabled?: boolean;
  style?: React.CSSProperties;
  className?: string;
  ariaLabel?: string;
}) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const listId = useId();
  const current = options.find((o) => o.value === value);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  useEffect(() => {
    if (open) setActive(Math.max(0, options.findIndex((o) => o.value === value)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  function choose(v: string) {
    onChange(v);
    setOpen(false);
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (disabled) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      if (!open) setOpen(true);
      else setActive((a) => Math.min(options.length - 1, a + 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(0, a - 1));
    } else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      if (open && options[active]) choose(options[active].value);
      else setOpen(true);
    } else if (e.key === "Escape") {
      setOpen(false);
    }
  }

  return (
    <div className={"combo select" + (className ? " " + className : "")} ref={rootRef} style={{ position: "relative", ...style }}>
      <button
        type="button"
        className="select-trigger"
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={ariaLabel}
        onClick={() => !disabled && setOpen((o) => !o)}
        onKeyDown={onKeyDown}
      >
        <span className={current ? undefined : "muted"}>{current ? current.label : placeholder ?? ""}</span>
        <svg className="select-caret" width="10" height="6" viewBox="0 0 10 6" aria-hidden>
          <path d="M1 1l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      {open && (
        <ul className="combo-list" role="listbox" id={listId}>
          {options.map((o, i) => (
            <li
              key={o.value}
              role="option"
              aria-selected={o.value === value}
              className={"combo-opt" + (i === active ? " active" : "")}
              onMouseDown={(e) => {
                e.preventDefault();
                choose(o.value);
              }}
              onMouseEnter={() => setActive(i)}
            >
              {o.label}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
