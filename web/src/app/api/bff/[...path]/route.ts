import { NextRequest, NextResponse } from "next/server";
import { upstreamHeaders } from "@/lib/server-auth";

// Backend-for-frontend: the browser talks only to this proxy; the proxy adds
// the credentials from the session cookie and calls the Go API. The tenant is
// derived by the API from those credentials, so there is no way for client
// code, a URL, a form field or a header to choose or change it.
const API_URL = process.env.API_URL ?? "http://api:8080";
const ALLOWED = new Set(["accounts", "contacts", "opportunities", "activities", "pipelines", "stages", "leads", "search", "dashboard", "users", "teams", "suppressions", "whoami"]);

function containsTenantKey(v: unknown): boolean {
  if (Array.isArray(v)) return v.some(containsTenantKey);
  if (v && typeof v === "object") {
    return Object.entries(v).some(([k, x]) => k.toLowerCase() === "tenant_id" || containsTenantKey(x));
  }
  return false;
}

const json = (status: number, error: string) => NextResponse.json({ error }, { status });

async function proxy(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  const { path } = await ctx.params;
  if (!path?.length || !ALLOWED.has(path[0])) return json(404, "not found");

  const creds = await upstreamHeaders();
  if (!creds) return json(401, "not authenticated");

  const url = new URL(req.url);
  if ([...url.searchParams.keys()].some((k) => k.toLowerCase() === "tenant_id")) {
    return json(400, "tenant_id is derived from your session and cannot be sent");
  }
  let body: string | undefined;
  if (req.method !== "GET" && req.method !== "HEAD") {
    body = await req.text();
    if (body) {
      try {
        if (containsTenantKey(JSON.parse(body))) return json(400, "tenant_id is derived from your session and cannot be sent");
      } catch {
        return json(400, "body must be valid JSON");
      }
    }
  }

  const target =
    path[0] === "whoami"
      ? `${API_URL}/api/whoami`
      : `${API_URL}/api/v1/${path.map(encodeURIComponent).join("/")}${url.search}`;
  // Only these headers are sent upstream: everything else the browser sent
  // (cookies, X-Tenant-Id, Authorization, ...) is dropped.
  const upstream = await fetch(target, {
    method: req.method,
    headers: { ...creds, ...(body ? { "Content-Type": "application/json" } : {}) },
    body,
    cache: "no-store",
  });
  const text = await upstream.text();
  return new NextResponse(text || null, {
    status: upstream.status,
    headers: { "Content-Type": upstream.headers.get("content-type") ?? "application/json" },
  });
}

export { proxy as GET, proxy as POST, proxy as PATCH, proxy as DELETE };
