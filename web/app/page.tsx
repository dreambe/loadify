"use client";

import { useEffect } from "react";
import { getToken } from "@/lib/auth";

export default function Home() {
  useEffect(() => {
    window.location.href = getToken() ? "/runs" : "/login";
  }, []);
  return null;
}
