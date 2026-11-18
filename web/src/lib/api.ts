import "server-only";
import { notFound, redirect } from "next/navigation";
import { upstreamHeaders } from "@/lib/server-auth";
import { ApiError, toApiError } from "@/lib/errors";

const API_URL = process.env.API_URL ?? "http://api:8080";

/** Server-side GET against the Go API using the session's credentials. */
export async function apiGet<T>(path: string): Promise<T> {
  const headers = await upstreamHeaders();
  if (!headers) redirect("/login");
  const res = await fetch(API_URL + path, { headers, cache: "no-store" });
  if (res.status === 401) redirect("/login");
  if (res.status === 404) notFound();
  if (!res.ok) throw await toApiError(res);
  return (await res.json()) as T;
}

/** Like apiGet but returns null instead of 404-ing (optional data). */
export async function apiGetOptional<T>(path: string): Promise<T | null> {
  try {
    return await apiGet<T>(path);
  } catch (e) {
    if (e instanceof ApiError && (e.status === 403 || e.status === 404)) return null;
    throw e;
  }
}
