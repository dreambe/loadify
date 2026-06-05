"use client";

import Link from "next/link";
import { clearSession, getUser } from "@/lib/auth";

export default function Nav() {
  const user = getUser();
  return (
    <nav className="nav">
      <Link className="brand" href="/runs">
        loadify
      </Link>
      <Link href="/runs">Runs</Link>
      <Link href="/tests">Tests</Link>
      <Link href="/workers">Workers</Link>
      {user?.role === "admin" && <Link href="/users">Users</Link>}
      <span className="spacer" />
      {user && (
        <span className="me">
          {user.name || user.email} · {user.role}
        </span>
      )}
      <button
        className="secondary"
        onClick={() => {
          clearSession();
          window.location.href = "/login";
        }}
      >
        Sign out
      </button>
    </nav>
  );
}
