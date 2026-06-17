"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n, roleLabel } from "@/lib/i18n";
import Help from "@/components/Help";
import { useToast } from "@/components/Toast";
import { useConfirm } from "@/components/Confirm";
import type { AuditEntry, User } from "@/lib/types";

export default function UsersPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const toast = useToast();
  const confirm = useConfirm();
  const [users, setUsers] = useState<User[]>([]);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState("viewer");
  const [password, setPassword] = useState("");
  const [creating, setCreating] = useState(false);

  const isAdmin = roleAtLeast(user?.role, "admin");

  function refresh() {
    api.listUsers().then(setUsers).catch((e) => toast.error(e.message));
  }
  useEffect(() => {
    if (ready && isAdmin) refresh();
  }, [ready, isAdmin]);

  function flash(msg: string) {
    toast.success(msg);
  }

  async function create(e: React.FormEvent) {
    e.preventDefault();
    if (creating) return;
    setCreating(true);
    try {
      await api.createUser({ email, name, role, password });
      setEmail("");
      setName("");
      setPassword("");
      toast.success(t("users.created"));
      refresh();
    } catch (e: any) {
      toast.error(e.message);
    } finally {
      setCreating(false);
    }
  }

  async function changeRole(u: User, newRole: string) {
    try {
      await api.updateUser(u.id, { role: newRole });
      refresh();
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  async function resetPassword(u: User) {
    const pw = window.prompt(t("users.resetPrompt").replace("{email}", u.email));
    if (!pw) return;
    try {
      await api.updateUser(u.id, { password: pw });
      toast.success(t("users.resetDone"));
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  async function toggleDisabled(u: User) {
    // Disabling locks the user out — confirm. Enabling is harmless, no prompt.
    if (
      !u.disabled &&
      !(await confirm({ title: t("users.disable") + " · " + u.email, danger: true, confirmLabel: t("users.disable") }))
    ) {
      return;
    }
    try {
      await api.updateUser(u.id, { disabled: !u.disabled });
      refresh();
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  async function remove(u: User) {
    const okToDelete = await confirm({
      title: t("users.delete") + " · " + u.email,
      body: t("users.deleteConfirm").replace("{email}", u.email),
      confirmLabel: t("users.delete"),
      danger: true,
    });
    if (!okToDelete) return;
    try {
      await api.deleteUser(u.id);
      toast.success(t("users.deleted"));
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
        <h1>{t("users.title")}</h1>

        <ProfileCard />

        <ChangePasswordPanel onError={toast.error} onDone={() => flash(t("users.pwChanged"))} />

        <WebhooksPanel onError={toast.error} onDone={() => flash(t("users.webhooksSaved"))} />

        {isAdmin && (
          <>
            <form className="panel" onSubmit={create}>
              <h2>{t("users.new")}</h2>
              <div className="row">
                <div>
                  <label>{t("users.email")}</label>
                  <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
                </div>
                <div>
                  <label>{t("users.name")}</label>
                  <input value={name} onChange={(e) => setName(e.target.value)} />
                </div>
                <div>
                  <label>{t("users.role")}</label>
                  <select value={role} onChange={(e) => setRole(e.target.value)}>
                    {["viewer", "operator", "admin"].map((r) => (
                      <option key={r} value={r}>{roleLabel(t, r)}</option>
                    ))}
                  </select>
                </div>
                <div>
                  <label>{t("users.password")}</label>
                  <input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    minLength={8}
                    required
                  />
                </div>
                <button type="submit" disabled={creating}>{t("users.create")}</button>
              </div>
            </form>

            <div className="panel">
              <table>
                <thead>
                  <tr>
                    <th>{t("users.colEmail")}</th>
                    <th>{t("users.colName")}</th>
                    <th>{t("users.colRole")}</th>
                    <th>{t("users.colStatus")}</th>
                    <th>{t("users.colLastLogin")}</th>
                    <th>{t("users.colActions")}</th>
                  </tr>
                </thead>
                <tbody>
                  {users.map((u) => {
                    const self = u.id === user?.id;
                    return (
                      <tr key={u.id} style={{ opacity: u.disabled ? 0.55 : 1 }}>
                        <td>
                          {u.email}
                          {self && <span className="muted"> ({t("users.you")})</span>}
                        </td>
                        <td>{u.name}</td>
                        <td>
                          <select
                            value={u.role}
                            disabled={self}
                            onChange={(e) => changeRole(u, e.target.value)}
                          >
                            {["viewer", "operator", "admin"].map((r) => (
                      <option key={r} value={r}>{roleLabel(t, r)}</option>
                    ))}
                          </select>
                        </td>
                        <td>
                          {u.disabled ? (
                            <span className="badge failed">{t("users.disabled")}</span>
                          ) : (
                            <span className="badge ok">{t("users.active")}</span>
                          )}
                        </td>
                        <td className="muted">
                          {u.last_login_at ? new Date(u.last_login_at).toLocaleString() : "–"}
                        </td>
                        <td>
                          <div className="actions">
                            <button className="ghost sm" onClick={() => resetPassword(u)}>
                              {t("users.resetPw")}
                            </button>
                            <button className="ghost sm" disabled={self} onClick={() => toggleDisabled(u)}>
                              {u.disabled ? t("users.enable") : t("users.disable")}
                            </button>
                            <button className="danger sm" disabled={self} onClick={() => remove(u)}>
                              {t("users.delete")}
                            </button>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                  {users.length === 0 && (
                    <tr>
                      <td colSpan={6} className="muted">
                        {t("users.empty")}
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>

            <AuditPanel onError={toast.error} />
          </>
        )}
      </div>
    </>
  );
}

// AuditPanel shows the recent record of mutating actions (who/when/what/outcome)
// across the platform. Admin-only — reads are not recorded, only changes.
function AuditPanel({ onError }: { onError: (m: string) => void }) {
  const { t } = useI18n();
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    api
      .listAudit()
      .then(setEntries)
      .catch((e) => onError(e.message))
      .finally(() => setLoaded(true));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Collapse the API path into a human-readable action label, keeping the raw
  // method+path available as a tooltip for precision.
  function action(e: AuditEntry): string {
    const ok = e.status >= 200 && e.status < 300;
    const verb =
      e.method === "POST" ? t("audit.create")
      : e.method === "PUT" || e.method === "PATCH" ? t("audit.update")
      : e.method === "DELETE" ? t("audit.delete")
      : e.method;
    const resource = e.path.replace(/^\/api\/v1\//, "").replace(/\/[0-9a-f-]{8,}/gi, "");
    const key = `audit.res.${resource}`;
    const label = t(key) === key ? resource : t(key); // localize known resources
    return `${verb} ${label}${ok ? "" : ` (${e.status})`}`;
  }

  return (
    <div className="panel">
      <h2>
        {t("audit.title")}
        <Help tip={t("audit.help")} />
      </h2>
      <div className="table-scroll">
        <table>
          <thead>
            <tr>
              <th>{t("audit.colTime")}</th>
              <th>{t("audit.colUser")}</th>
              <th>{t("audit.colAction")}</th>
              <th className="num">{t("audit.colStatus")}</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e) => {
              const ok = e.status >= 200 && e.status < 300;
              return (
                <tr key={e.id}>
                  <td className="muted" style={{ whiteSpace: "nowrap" }}>
                    {new Date(e.ts).toLocaleString()}
                  </td>
                  <td>{e.user_name || "–"}</td>
                  <td title={`${e.method} ${e.path}`} style={{ fontFamily: "var(--font-mono)", fontSize: 12.5 }}>
                    {action(e)}
                  </td>
                  <td className="num">
                    <span className={"badge " + (ok ? "completed" : "failed")}>{e.status}</span>
                  </td>
                </tr>
              );
            })}
            {loaded && entries.length === 0 && (
              <tr>
                <td colSpan={4} className="muted">
                  {t("audit.empty")}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// ProfileCard shows the signed-in user's identity: avatar (Feishu's when
// present, otherwise an initial), role, and account timestamps.
function ProfileCard() {
  const { t } = useI18n();
  const [me, setMe] = useState<User | null>(null);
  useEffect(() => {
    api.me().then(setMe).catch(() => {});
  }, []);
  if (!me) return null;
  const initial = (me.name || me.email || "?").trim().charAt(0).toUpperCase();
  return (
    <div className="panel">
      <div className="row" style={{ alignItems: "center", gap: 16 }}>
        {me.avatar_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img className="avatar" src={me.avatar_url} alt={me.name || me.email} />
        ) : (
          <span className="avatar fallback">{initial}</span>
        )}
        <div style={{ flex: 1 }}>
          <div style={{ fontWeight: 700, fontSize: 16 }}>{me.name || me.email}</div>
          <div className="muted" style={{ fontSize: 13 }}>
            {me.email}
          </div>
        </div>
        <span className="badge completed">{roleLabel(t, me.role)}</span>
        <div className="muted" style={{ fontSize: 12.5, textAlign: "right" }}>
          <div>
            {t("users.profileCreated")}:{" "}
            {me.created_at ? new Date(me.created_at).toLocaleDateString() : "–"}
          </div>
          <div>
            {t("users.colLastLogin")}:{" "}
            {me.last_login_at ? new Date(me.last_login_at).toLocaleString() : "–"}
          </div>
        </div>
      </div>
    </div>
  );
}

// WebhooksPanel lets the signed-in user manage their notification webhooks.
// When one of their runs finishes/auto-stops, the first URL is notified
// (Feishu/Lark bot URLs get a formatted card).
function WebhooksPanel({ onError, onDone }: { onError: (m: string) => void; onDone: () => void }) {
  const { t } = useI18n();
  const [urls, setUrls] = useState<string[]>([]); // saved
  const [draft, setDraft] = useState<string[]>([]); // being edited
  const [editing, setEditing] = useState(false);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    api
      .getWebhooks()
      .then((r) => setUrls(r.webhook_urls ?? []))
      .catch(() => setUrls([]))
      .finally(() => setLoaded(true));
  }, []);
  if (!loaded) return null;

  const startEdit = () => {
    setDraft(urls.length ? urls : [""]);
    setEditing(true);
  };

  async function save() {
    try {
      const cleaned = await api.setWebhooks(draft.map((u) => u.trim()).filter(Boolean));
      setUrls(cleaned.webhook_urls ?? []);
      setEditing(false);
      onDone();
    } catch (e: any) {
      onError(e.message);
    }
  }

  return (
    <div className="panel">
      <h2>
        {t("users.webhooks")}
        <Help tip={t("users.webhooksHelp")} />
      </h2>
      {!editing ? (
        // Saved state: read-only display, not raw inputs, so it's obvious the
        // value is persisted. Editing is an explicit, opt-in action.
        <>
          {urls.length === 0 ? (
            <p className="muted">{t("users.webhookNone")}</p>
          ) : (
            urls.map((u, i) => (
              <div
                key={i}
                style={{
                  fontFamily: "var(--font-mono)",
                  fontSize: 13,
                  padding: "8px 10px",
                  border: "1px solid var(--border)",
                  borderRadius: 6,
                  marginBottom: 6,
                  wordBreak: "break-all",
                  background: "var(--panel-2, transparent)",
                }}
              >
                {u}
              </div>
            ))
          )}
          <div className="row" style={{ marginTop: 8 }}>
            <button type="button" className="secondary" onClick={startEdit}>
              {t("users.webhookEdit")}
            </button>
          </div>
        </>
      ) : (
        <>
          {draft.map((u, i) => (
            <div className="row" key={i} style={{ marginBottom: 6 }}>
              <input
                value={u}
                placeholder="https://open.feishu.cn/open-apis/bot/v2/hook/…"
                onChange={(e) => setDraft(draft.map((x, idx) => (idx === i ? e.target.value : x)))}
                style={{ flex: 1, fontFamily: "var(--font-mono)" }}
              />
              <button
                type="button"
                className="secondary"
                onClick={() => {
                  const next = draft.filter((_, idx) => idx !== i);
                  setDraft(next.length ? next : [""]);
                }}
              >
                {t("ramp.remove")}
              </button>
            </div>
          ))}
          <div className="row" style={{ marginTop: 8 }}>
            <button type="button" className="secondary" onClick={() => setDraft([...draft, ""])}>
              + {t("users.webhookAdd")}
            </button>
            <button type="button" onClick={save}>
              {t("users.webhookSave")}
            </button>
            <button type="button" className="ghost" onClick={() => setEditing(false)}>
              {t("users.webhookCancel")}
            </button>
          </div>
        </>
      )}
      <p className="muted" style={{ fontSize: 12.5 }}>
        {t("users.webhookDefault")}
      </p>
    </div>
  );
}

// ChangePasswordPanel lets the signed-in user rotate their own password.
function ChangePasswordPanel({
  onError,
  onDone,
}: {
  onError: (m: string) => void;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const [oldPw, setOldPw] = useState("");
  const [newPw, setNewPw] = useState("");

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    try {
      await api.changePassword(oldPw, newPw);
      setOldPw("");
      setNewPw("");
      onDone();
    } catch (e: any) {
      onError(e.message);
    }
  }

  return (
    <form className="panel" onSubmit={submit}>
      <h2>{t("users.myPassword")}</h2>
      <div className="row">
        <div>
          <label>{t("users.oldPassword")}</label>
          <input type="password" value={oldPw} onChange={(e) => setOldPw(e.target.value)} />
        </div>
        <div>
          <label>{t("users.newPassword")}</label>
          <input
            type="password"
            value={newPw}
            onChange={(e) => setNewPw(e.target.value)}
            minLength={8}
            required
          />
        </div>
        <button type="submit">{t("users.changePw")}</button>
      </div>
    </form>
  );
}
