"use client";

import { useEffect, useState } from "react";
import type { Role, User } from "./types";

const TOKEN_KEY = "loadify_token";
const USER_KEY = "loadify_user";

export function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(TOKEN_KEY);
}

export function setSession(token: string, user: User) {
  window.localStorage.setItem(TOKEN_KEY, token);
  window.localStorage.setItem(USER_KEY, JSON.stringify(user));
}

export function setToken(token: string) {
  window.localStorage.setItem(TOKEN_KEY, token);
}

export function getUser(): User | null {
  if (typeof window === "undefined") return null;
  const raw = window.localStorage.getItem(USER_KEY);
  return raw ? (JSON.parse(raw) as User) : null;
}

export function clearSession() {
  window.localStorage.removeItem(TOKEN_KEY);
  window.localStorage.removeItem(USER_KEY);
}

export function roleAtLeast(role: Role | undefined, min: Role): boolean {
  const rank: Record<Role, number> = { viewer: 1, operator: 2, admin: 3 };
  return !!role && rank[role] >= rank[min];
}

// ownsOrAdmin mirrors the backend "owner-or-admin write" policy: a user may
// modify a resource only if they created it or are an admin. Used to disable
// (not hide) mutating controls for non-owners so the UI matches what the API
// will allow.
export function ownsOrAdmin(user: User | null | undefined, createdBy?: string): boolean {
  if (!user) return false;
  return user.role === "admin" || (!!createdBy && createdBy === user.id);
}

// useAuth exposes the current user and a loading flag, redirecting to /login
// when no token is present.
export function useAuth(redirect = true) {
  const [user, setUser] = useState<User | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const t = getToken();
    if (!t) {
      if (redirect && typeof window !== "undefined") window.location.href = "/login";
      setReady(true);
      return;
    }
    setUser(getUser());
    setReady(true);
  }, [redirect]);

  return { user, ready };
}
