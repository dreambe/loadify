import { useEffect, type RefObject } from "react";

// focusablesIn returns the tabbable elements inside a container, in DOM order,
// skipping anything not currently rendered/visible.
function focusablesIn(root: HTMLElement): HTMLElement[] {
  return Array.from(
    root.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )
  ).filter((el) => el.offsetParent !== null || el === document.activeElement);
}

// useFocusTrap makes an overlay dialog behave for keyboard and screen-reader
// users: Escape closes it, Tab cycles focus within it (never escaping to the
// page behind), focus moves into it on open, and returns to the previously
// focused element on close. It also locks body scroll while open. Shared by
// Modal and InspectDrawer so both dialogs behave identically.
export function useFocusTrap(ref: RefObject<HTMLElement>, onClose: () => void) {
  useEffect(() => {
    const prevFocus = document.activeElement as HTMLElement | null;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onClose();
        return;
      }
      if (e.key === "Tab" && ref.current) {
        const items = focusablesIn(ref.current);
        if (items.length === 0) {
          e.preventDefault();
          return;
        }
        const first = items[0];
        const last = items[items.length - 1];
        const active = document.activeElement;
        if (e.shiftKey && active === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && active === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", onKey);
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    // Move focus into the dialog once it's mounted.
    const items = ref.current ? focusablesIn(ref.current) : [];
    (items[0] ?? ref.current)?.focus();
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prevOverflow;
      prevFocus?.focus?.();
    };
  }, [ref, onClose]);
}
