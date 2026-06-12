"use client";

// Help renders a small "?" marker that reveals an explanation bubble on hover
// or keyboard focus — used to explain load-testing jargon inline.
export default function Help({ tip }: { tip: string }) {
  return (
    <span className="help" data-tip={tip} tabIndex={0} role="note" aria-label={tip}>
      ?
    </span>
  );
}
