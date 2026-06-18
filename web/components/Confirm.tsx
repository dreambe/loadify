"use client";

import { createContext, useCallback, useContext, useState } from "react";
import { useI18n } from "@/lib/i18n";

interface ConfirmOpts {
  title: string;
  body?: string;
  confirmLabel?: string;
  danger?: boolean;
}

type ConfirmFn = (opts: ConfirmOpts) => Promise<boolean>;

const ConfirmContext = createContext<ConfirmFn>(async () => false);

// ConfirmProvider replaces the browser's window.confirm with a styled dialog.
// Usage: const confirm = useConfirm(); if (await confirm({title})) { ... }
export function ConfirmProvider({ children }: { children: React.ReactNode }) {
  const { t } = useI18n();
  const [opts, setOpts] = useState<ConfirmOpts | null>(null);
  const [resolver, setResolver] = useState<((v: boolean) => void) | null>(null);

  const confirm = useCallback<ConfirmFn>((o) => {
    setOpts(o);
    return new Promise<boolean>((resolve) => setResolver(() => resolve));
  }, []);

  const close = (v: boolean) => {
    resolver?.(v);
    setResolver(null);
    setOpts(null);
  };

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      {opts && (
        <div className="modal-backdrop" onClick={() => close(false)}>
          <div className="modal" onClick={(e) => e.stopPropagation()} role="alertdialog" aria-modal="true">
            <h2>{opts.title}</h2>
            {opts.body && <p className="muted" style={{ marginTop: 0 }}>{opts.body}</p>}
            <div className="modal-actions">
              {/* On a danger dialog focus Cancel so a stray Enter doesn't
                  immediately trigger the destructive action; otherwise focus
                  Confirm for quick keyboard acceptance. */}
              <button className="ghost" onClick={() => close(false)} autoFocus={opts.danger}>
                {t("common.cancel")}
              </button>
              <button className={opts.danger ? "danger" : ""} onClick={() => close(true)} autoFocus={!opts.danger}>
                {opts.confirmLabel || t("common.confirm")}
              </button>
            </div>
          </div>
        </div>
      )}
    </ConfirmContext.Provider>
  );
}

export function useConfirm() {
  return useContext(ConfirmContext);
}
