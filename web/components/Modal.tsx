"use client";

import { useEffect, type ReactNode } from "react";
import { createPortal } from "react-dom";
import Icon from "./Icon";

// Modal is a centered overlay dialog. Closes on backdrop click, the ✕ button, or
// Escape. Used for the expanded (fullscreen) chart view.
export default function Modal({
  title,
  onClose,
  children,
  wide,
}: {
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  wide?: boolean;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [onClose]);

  if (typeof document === "undefined") return null;

  // Portal to <body>: a transformed ancestor (the page-transition wrapper) would
  // otherwise become the containing block for position:fixed and push the modal
  // off-center. Rendering at the body root keeps it viewport-centered.
  return createPortal(
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className={"modal-panel" + (wide ? " wide" : "")}
        role="dialog"
        aria-modal="true"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal-head">
          <h2 style={{ margin: 0 }}>{title}</h2>
          <button className="ghost sm" aria-label="close" onClick={onClose}>
            <Icon name="x" size={16} />
          </button>
        </div>
        <div className="modal-body">{children}</div>
      </div>
    </div>,
    document.body
  );
}
