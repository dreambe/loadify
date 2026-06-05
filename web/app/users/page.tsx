"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import type { User } from "@/lib/types";

export default function UsersPage() {
  const { user, ready } = useAuth();
  const [users, setUsers] = useState<User[]>([]);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState("viewer");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");

  function refresh() {
    api.listUsers().then(setUsers).catch((e) => setErr(e.message));
  }
  useEffect(() => {
    if (ready && roleAtLeast(user?.role, "admin")) refresh();
  }, [ready, user]);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    try {
      await api.createUser({ email, name, role, password });
      setEmail("");
      setName("");
      setPassword("");
      refresh();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  if (!ready) return null;
  if (!roleAtLeast(user?.role, "admin")) {
    return (
      <>
        <Nav />
        <div className="container">
          <p className="error">Admin access required.</p>
        </div>
      </>
    );
  }

  return (
    <>
      <Nav />
      <div className="container">
        <h1>Users</h1>
        <form className="panel" onSubmit={create}>
          <h2>New user</h2>
          <div className="row">
            <div>
              <label>Email</label>
              <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
            </div>
            <div>
              <label>Name</label>
              <input value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div>
              <label>Role</label>
              <select value={role} onChange={(e) => setRole(e.target.value)}>
                {["viewer", "operator", "admin"].map((r) => (
                  <option key={r}>{r}</option>
                ))}
              </select>
            </div>
            <div>
              <label>Password</label>
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
            </div>
            <button type="submit">Create</button>
          </div>
          {err && <div className="error">{err}</div>}
        </form>

        <div className="panel">
          <table>
            <thead>
              <tr>
                <th>Email</th>
                <th>Name</th>
                <th>Role</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id}>
                  <td>{u.email}</td>
                  <td>{u.name}</td>
                  <td>{u.role}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </>
  );
}
