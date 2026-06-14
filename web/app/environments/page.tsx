"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import EmptyState from "@/components/EmptyState";
import TableSkeleton from "@/components/TableSkeleton";
import Help from "@/components/Help";
import { useToast } from "@/components/Toast";
import { useConfirm } from "@/components/Confirm";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast, ownsOrAdmin } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import type { Environment } from "@/lib/types";

type Pair = { key: string; value: string };

export default function EnvironmentsPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const toast = useToast();
  const confirm = useConfirm();
  const [envs, setEnvs] = useState<Environment[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [editing, setEditing] = useState<Environment | "new" | null>(null);
  const [name, setName] = useState("");
  const [pairs, setPairs] = useState<Pair[]>([{ key: "", value: "" }]);

  const canEdit = roleAtLeast(user?.role, "operator");

  function refresh() {
    api.listEnvironments().then(setEnvs).catch((e) => toast.error(e.message)).finally(() => setLoaded(true));
  }
  useEffect(() => {
    if (ready) refresh();
  }, [ready]);

  function openNew() {
    setEditing("new");
    setName("");
    setPairs([{ key: "base_url", value: "" }]);
  }
  function openEdit(env: Environment) {
    setEditing(env);
    setName(env.name);
    const ps = Object.entries(env.vars).map(([key, value]) => ({ key, value }));
    setPairs(ps.length ? ps : [{ key: "", value: "" }]);
  }

  async function save() {
    if (!name.trim()) {
      toast.error(t("env.errName"));
      return;
    }
    const vars: Record<string, string> = {};
    for (const p of pairs) if (p.key.trim()) vars[p.key.trim()] = p.value;
    try {
      if (editing === "new") {
        await api.createEnvironment(name, vars);
        toast.success(t("env.created"));
      } else if (editing) {
        await api.updateEnvironment(editing.id, name, vars);
        toast.success(t("env.updated"));
      }
      setEditing(null);
      refresh();
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  async function remove(env: Environment) {
    if (!(await confirm({ title: t("env.delete") + " · " + env.name, danger: true, confirmLabel: t("env.delete") }))) return;
    try {
      await api.deleteEnvironment(env.id);
      toast.success(t("env.deleted"));
      refresh();
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  if (!ready) return null;

  return (
    <>
      <Nav />
      <div className="container">
        <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
          <h1 style={{ margin: 0 }}>
            {t("env.title")}
            <Help tip={t("env.help")} />
          </h1>
          {canEdit && <button onClick={openNew}>+ {t("env.new")}</button>}
        </div>
        <div style={{ height: 16 }} />

        {editing && (
          <div className="panel">
            <h2>{editing === "new" ? t("env.new") : t("env.editTitle")}</h2>
            <div className="field" style={{ maxWidth: 360 }}>
              <label className="req">{t("env.name")}</label>
              <input value={name} onChange={(e) => setName(e.target.value)} placeholder="dev / prod / …" />
            </div>
            <label>{t("env.vars")}</label>
            {pairs.map((p, i) => (
              <div className="row" key={i} style={{ marginBottom: 6 }}>
                <input
                  placeholder="KEY (如 base_url)"
                  value={p.key}
                  onChange={(e) => setPairs(pairs.map((x, idx) => (idx === i ? { ...x, key: e.target.value } : x)))}
                  style={{ width: 220, fontFamily: "var(--font-mono)" }}
                />
                <input
                  placeholder="value"
                  value={p.value}
                  onChange={(e) => setPairs(pairs.map((x, idx) => (idx === i ? { ...x, value: e.target.value } : x)))}
                  style={{ flex: 1, fontFamily: "var(--font-mono)" }}
                />
                <button
                  type="button"
                  className="secondary"
                  onClick={() => setPairs(pairs.filter((_, idx) => idx !== i).length ? pairs.filter((_, idx) => idx !== i) : [{ key: "", value: "" }])}
                >
                  {t("ramp.remove")}
                </button>
              </div>
            ))}
            <div className="row" style={{ marginTop: 8 }}>
              <button type="button" className="secondary" onClick={() => setPairs([...pairs, { key: "", value: "" }])}>
                + {t("env.addVar")}
              </button>
            </div>
            <p className="muted" style={{ fontSize: 12.5 }}>
              {t("env.usageHint")}
            </p>
            <div className="row" style={{ marginTop: 8 }}>
              <button onClick={save}>{t("tests.save")}</button>
              <button className="ghost" onClick={() => setEditing(null)}>
                {t("common.cancel")}
              </button>
            </div>
          </div>
        )}

        <div className="panel">
          {!loaded ? (
            <TableSkeleton cols={canEdit ? 4 : 3} />
          ) : envs.length === 0 ? (
            <EmptyState title={t("env.empty")} hint={canEdit ? t("env.emptyHint") : undefined} />
          ) : (
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>{t("env.name")}</th>
                    <th>{t("env.colVars")}</th>
                    <th>{t("env.colCreator")}</th>
                    {canEdit && <th className="num">{t("tests.colActions")}</th>}
                  </tr>
                </thead>
                <tbody>
                  {envs.map((env) => (
                    <tr key={env.id}>
                      <td>{env.name}</td>
                      <td className="muted" style={{ fontFamily: "var(--font-mono)", fontSize: 12 }}>
                        {Object.keys(env.vars).slice(0, 6).join(", ") || "–"}
                      </td>
                      <td className="muted">{env.creator_name || "–"}</td>
                      {canEdit && (
                        <td>
                          <div className="actions">
                            {(() => {
                              const owns = ownsOrAdmin(user, env.created_by);
                              const why = owns ? undefined : t("common.ownerOnly");
                              return (
                                <>
                                  <button className="ghost sm" disabled={!owns} title={why} onClick={() => openEdit(env)}>
                                    {t("tests.edit")}
                                  </button>
                                  <button className="danger sm" disabled={!owns} title={why} onClick={() => remove(env)}>
                                    {t("env.delete")}
                                  </button>
                                </>
                              );
                            })()}
                          </div>
                        </td>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </>
  );
}
