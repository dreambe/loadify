"use client";

import { useEffect, useState } from "react";

// NumberInput is a controlled integer input that doesn't fight the user while
// they type. The naive pattern — parseInt(e.target.value || "1") on every
// keystroke — snaps the field back to a default the moment it's cleared, so
// replacing "100" with "50" required editing around the forced value. Here the
// field may be transiently empty or partial; valid numbers propagate as typed,
// and blur clamps to [min, max] (or restores the last valid value).
export default function NumberInput({
  value,
  onChange,
  min,
  max,
  float,
  style,
  disabled,
  placeholder,
}: {
  value: number;
  onChange: (n: number) => void;
  min?: number;
  max?: number;
  float?: boolean; // accept decimals (default: integers only)
  style?: React.CSSProperties;
  disabled?: boolean;
  placeholder?: string;
}) {
  const [text, setText] = useState(String(value));
  const parse = (s: string) => (float ? parseFloat(s) : parseInt(s, 10));

  // Sync from the outside (a preset button, a reset) without clobbering the
  // user's in-progress text when the change originated here.
  useEffect(() => {
    setText((cur) => (parse(cur) === value ? cur : String(value)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);

  const clamp = (n: number) =>
    Math.min(max ?? Number.POSITIVE_INFINITY, Math.max(min ?? Number.NEGATIVE_INFINITY, n));

  return (
    <input
      type="number"
      inputMode={float ? "decimal" : "numeric"}
      min={min}
      max={max}
      value={text}
      disabled={disabled}
      placeholder={placeholder}
      style={style}
      onChange={(e) => {
        setText(e.target.value);
        const n = parse(e.target.value);
        if (!Number.isNaN(n)) onChange(n);
      }}
      onBlur={() => {
        const n = parse(text);
        const v = Number.isNaN(n) ? clamp(value) : clamp(n);
        setText(String(v));
        if (v !== value) onChange(v);
      }}
    />
  );
}
