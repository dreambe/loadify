"use client";

import { useI18n } from "@/lib/i18n";
import NumberInput from "./NumberInput";
import type { Threshold } from "@/lib/types";

const METRICS = ["p50_ms", "p90_ms", "p95_ms", "p99_ms", "error_rate", "qps"];
const OPS = ["<", "<=", ">", ">="];

// ThresholdsEditor edits SLA pass/fail criteria (k6-style): metric op value.
// error_rate is expressed in percent; latencies in ms; qps in req/s.
export default function ThresholdsEditor({
  value,
  onChange,
}: {
  value: Threshold[];
  onChange: (t: Threshold[]) => void;
}) {
  const { t } = useI18n();

  function update(i: number, patch: Partial<Threshold>) {
    onChange(value.map((th, idx) => (idx === i ? { ...th, ...patch } : th)));
  }
  function add() {
    onChange([...value, { metric: "p95_ms", op: "<", value: 200 }]);
  }
  function remove(i: number) {
    onChange(value.filter((_, idx) => idx !== i));
  }

  return (
    <div>
      {value.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>{t("sla.metric")}</th>
              <th>{t("sla.op")}</th>
              <th>{t("sla.value")}</th>
              <th style={{ width: 80 }}></th>
            </tr>
          </thead>
          <tbody>
            {value.map((th, i) => (
              <tr key={i}>
                <td>
                  <select value={th.metric} onChange={(e) => update(i, { metric: e.target.value })}>
                    {METRICS.map((m) => (
                      <option key={m}>{m}</option>
                    ))}
                  </select>
                </td>
                <td>
                  <select value={th.op} onChange={(e) => update(i, { op: e.target.value })}>
                    {OPS.map((o) => (
                      <option key={o}>{o}</option>
                    ))}
                  </select>
                </td>
                <td>
                  <NumberInput float value={th.value} onChange={(n) => update(i, { value: n })} style={{ width: 120 }} />
                </td>
                <td>
                  <button type="button" className="secondary" onClick={() => remove(i)}>
                    {t("ramp.remove")}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <button type="button" className="secondary" onClick={add} style={{ marginTop: 8 }}>
        + {t("sla.add")}
      </button>
    </div>
  );
}
