import { ApiError, toApiError } from "@/lib/errors";

/** Browser-side call to the BFF proxy; throws ApiError carrying the API's message. */
export async function bff<T = unknown>(path: string, init?: { method?: string; body?: unknown }): Promise<T> {
  const res = await fetch(`/api/bff${path}`, {
    method: init?.method ?? "GET",
    headers: init?.body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: init?.body !== undefined ? JSON.stringify(init.body) : undefined,
    cache: "no-store",
  });
  if (res.status === 401) {
    window.location.href = "/login";
    throw new ApiError(401, "Your session has expired. Please sign in again.");
  }
  if (res.status === 204) return undefined as T;
  if (!res.ok) throw await toApiError(res);
  return (await res.json()) as T;
}
