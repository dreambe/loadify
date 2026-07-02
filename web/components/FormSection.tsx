"use client";

import type { ReactNode } from "react";

// FormSection gives a long form a numbered, scannable rhythm: an accent index,
// a title, an optional one-line hint, then the fields. Sections are separated
// by hairline rules rather than nested boxes, so builders that draw their own
// borders (ramp stages, scenario steps) don't end up boxed-in-boxes.
export default function FormSection({
  num,
  title,
  hint,
  children,
}: {
  num: string;
  title: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="form-section">
      <div className="fs-head">
        <span className="fs-num">{num}</span>
        <span className="fs-title">{title}</span>
      </div>
      {hint && <p className="fs-hint">{hint}</p>}
      <div className="fs-body">{children}</div>
    </section>
  );
}
