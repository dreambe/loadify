"use client";

import { useState } from "react";
import { useI18n } from "@/lib/i18n";

// JsonExplorer renders a parsed JSON value as an interactive tree. Hovering a
// node reveals its dot-path; clicking the pick button hands that path (plus the
// leaf key and a sample value) back to the caller, which turns it into a
// pre-filled extract or assert row. The generated path mirrors the runtime
// evaluators (internal/script _get and httpd lookupPath) and the existing
// evalAssertPreview walker: object segments use the key, array segments use the
// numeric index, joined by ".". Keys containing "." can't be expressed
// unambiguously, so those nodes are non-pickable and hint to fill manually.
export type JsonPickMode = "extract" | "assert";

// MAX_DEPTH guards against pathological nesting blowing up the DOM; deeper
// nodes are still shown but their children collapse behind a notice.
const MAX_DEPTH = 12;

function isLeaf(v: unknown): boolean {
  return v === null || typeof v !== "object";
}

// preview renders a compact inline value for leaves.
function preview(v: unknown): { text: string; cls: string } {
  if (v === null) return { text: "null", cls: "jx-null" };
  if (typeof v === "string") {
    const s = v.length > 60 ? v.slice(0, 60) + "…" : v;
    return { text: JSON.stringify(s), cls: "jx-str" };
  }
  if (typeof v === "number" || typeof v === "boolean") return { text: String(v), cls: "jx-num" };
  return { text: "", cls: "" };
}

function Node({
  k,
  value,
  path,
  pickable,
  depth,
  mode,
  onPick,
}: {
  k: string; // display key (object key, array index, or "" for root)
  value: unknown;
  path: string;
  pickable: boolean; // false once an ancestor key can't be expressed as a path
  depth: number;
  mode: JsonPickMode;
  onPick: (path: string, leafKey: string, sample: unknown) => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(depth < 2);
  const leaf = isLeaf(value);
  const isArr = Array.isArray(value);
  const entries: [string, unknown][] = leaf
    ? []
    : isArr
      ? (value as unknown[]).map((v, i) => [String(i), v])
      : Object.entries(value as Record<string, unknown>);

  const pv = leaf ? preview(value) : null;
  const summary = leaf ? null : isArr ? `[${entries.length}]` : `{${entries.length}}`;
  // The root row (path === "") has no key to pick and represents the whole body.
  const canPick = pickable && path !== "";

  return (
    <div className="jx-node">
      <div className="jx-row">
        {!leaf && entries.length > 0 ? (
          <button type="button" className="jx-toggle" onClick={() => setOpen((o) => !o)} aria-label="toggle">
            {open ? "▾" : "▸"}
          </button>
        ) : (
          <span className="jx-toggle-spacer" />
        )}
        {k !== "" && <span className="jx-key">{k}</span>}
        {k !== "" && <span className="jx-colon">:</span>}
        {leaf ? (
          <span className={`jx-val ${pv!.cls}`}>{pv!.text}</span>
        ) : (
          <span className="jx-summary">{summary}</span>
        )}
        {canPick ? (
          <>
            <span className="jx-path" title={path}>
              {path}
            </span>
            <button
              type="button"
              className="jx-pick"
              onClick={() => onPick(path, leafKeyOf(path), value)}
              title={path}
            >
              + {t(mode === "extract" ? "json.pickExtract" : "json.pickAssert")}
            </button>
          </>
        ) : (
          pickable === false && (
            <span className="jx-cant" title={t("json.cantAutoPath")}>
              {t("json.cantAutoPath")}
            </span>
          )
        )}
      </div>
      {!leaf && open && (
        <div className="jx-children">
          {depth >= MAX_DEPTH ? (
            <div className="jx-more">…</div>
          ) : (
            entries.map(([ck, cv]) => {
              // Object keys with "." (or empty) break dot-path addressing.
              const keyOk = isArr || (ck !== "" && !ck.includes("."));
              const childPath = path ? `${path}.${ck}` : ck;
              return (
                <Node
                  key={ck}
                  k={ck}
                  value={cv}
                  path={childPath}
                  pickable={pickable && keyOk}
                  depth={depth + 1}
                  mode={mode}
                  onPick={onPick}
                />
              );
            })
          )}
        </div>
      )}
    </div>
  );
}

// leafKeyOf returns the last path segment, used as the default variable name.
function leafKeyOf(path: string): string {
  const parts = path.split(".");
  return parts[parts.length - 1] || "value";
}

export default function JsonExplorer({
  body,
  mode,
  onPick,
}: {
  body: string;
  mode: JsonPickMode;
  onPick: (path: string, leafKey: string, sample: unknown) => void;
}) {
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    return null; // caller falls back to the raw <pre> view
  }
  return (
    <div className="jsonExplorer">
      <Node k="" value={parsed} path="" pickable depth={0} mode={mode} onPick={onPick} />
    </div>
  );
}
