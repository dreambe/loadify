"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import type { User } from "@/lib/types";

export default function UsersPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const [users, setUsers] = useState<User[]>([]);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState("viewer");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [ok, setOk] = useState("");

  const isAdmin = roleAtLeast(user?.role, "admin");

  function refresh() {
    api.listUsers().then(setUsers).catch((e) => setErr(e.message));
  }
  useEffect(() => {
    if (ready && isAdmin) refresh();
  }, [ready, isAdmin]);

  function flash(msg: string) {
    setErr("");
    setOk(msg);
    setTimeout(() => setOk(""), 3000);
  }

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    try {
      await api.createUser({ email, name, role, password });
      setEmail("");
      setName("");
      setPassword("");
      flash(t("users.created"));
      refresh();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  async function changeRole(u: User, newRole: string) {
    try {
      await api.updateUser(u.id, { role: newRole });
      refresh();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  async function resetPassword(u: User) {
    const pw = window.prompt(t("users.resetPrompt").replace("{email}", u.email));
    if (!pw) return;
    try {
      await api.updateUser(u.id, { password: pw });
      flash(t("users.resetDone"));
    } catch (e: any) {
      setErr(e.message);
    }
  }

  async function toggleDisabled(u: User) {
    try {
      await api.updateUser(u.id, { disabled: !u.disabled });
      refresh();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  async function remove(u: User) {
    if (!window.confirm(t("users.deleteConfirm").replace("{email}", u.email))) return;
    try {
      await api.deleteUser(u.id);
      refresh();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  if (!ready) return null;

  return (
    <>
      <Nav />
      <div className="container">
        <h1>{t("users.title")}</h1>

        <ChangePasswordPanel onError={setErr} onDone={() => flash(t("users.pwChanged"))} />

        {!isAdmin && err && <div className="error">{err}</div>}
        {!isAdmin && ok && <div style={{ color: "var(--green)" }}>{ok}</div>}

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
                      <option key={r}>{r}</option>
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
                <button type="submit">{t("users.create")}</button>
              </div>
              {err && <div className="error">{err}</div>}
              {ok && <div style={{ color: "var(--green)" }}>{ok}</div>}
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
                              <option key={r}>{r}</option>
                            ))}
                          </select>
                        </td>
                        <td>
                          {u.disabled ? (
                            <span className="badge failed">{t("users.disabled")}</span>
                          ) : (
                            <span className="badge completed">{t("users.active")}</span>
                          )}
                        </td>
                        <td className="muted">
                          {u.last_login_at ? new Date(u.last_login_at).toLocaleString() : "–"}
                        </td>
                        <td>
                          <div className="row" style={{ gap: 8 }}>
                            <button className="secondary" onClick={() => resetPassword(u)}>
                              {t("users.resetPw")}
                            </button>
                            <button className="secondary" disabled={self} onClick={() => toggleDisabled(u)}>
                              {u.disabled ? t("users.enable") : t("users.disable")}
                            </button>
                            <button className="secondary" disabled={self} onClick={() => remove(u)}>
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
          </>
        )}
      </div>
    </>
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
    onError("");
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
