"use client";

import { useEffect, useState } from "react";

// EntityPicker is the single searchable combobox used wherever the user picks a
// record by typing (tests on the runs page, runs on the compare page). It exists
// to kill the duplicated picker logic whose divergence caused real bugs (e.g. a
// copied full ID that the search couldn't resolve). The contract:
//   - label(item): the human-readable text shown in the dropdown and field.
//   - keys(item):  every string the typed/pasted value may match (id, short id,
//                  name, …). Matching is exact against label() or any key, so a
//                  value produced elsewhere (a copied ID) always resolves here.
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
  className,
  style,
}: EntityPickerProps<T>) {
  const [text, setText] = useState("");

  // Keep the field in sync with the selected value: show its label, or the raw
  // id when it's a valid selection outside the loaded list (don't blank it).
  useEffect(() => {
    const item = items.find((x) => idOf(x) === value);
    setText(item ? label(item) : value);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, items.length]);

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

  return (
    <>
      <input
        list={listId}
        value={text}
        placeholder={placeholder}
        onChange={(e) => {
          setText(e.target.value);
          onChange(resolve(e.target.value));
        }}
        className={className}
        style={style}
      />
      <datalist id={listId}>
        {items.map((x) => (
          <option key={idOf(x)} value={label(x)} />
        ))}
      </datalist>
    </>
  );
}
