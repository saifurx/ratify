import "server-only";
import { getToken } from "next-auth/jwt";
import { cookies } from "next/headers";

const COOKIE_NAMES = ["__Secure-authjs.session-token", "authjs.session-token"];

/**
 * Headers that authenticate the SERVER's request to the Go API. Credentials
 * come only from the encrypted session cookie. In OIDC mode that is the
 * verified ID token (the API derives the tenant from its tenant_id claim); in
 * dev mode the tenant/user/role from the demo login. Nothing the browser
 * sends is ever forwarded.
 */
export async function upstreamHeaders(): Promise<Record<string, string> | null> {
  const jar = await cookies();
  const name = COOKIE_NAMES.find((n) => jar.getAll().some((c) => c.name === n || c.name.startsWith(n + ".")));
  if (!name) return null;
  const token = await getToken({
    req: { headers: new Headers({ cookie: jar.toString() }) } as never,
    secret: process.env.AUTH_SECRET!,
    salt: name,
    secureCookie: name.startsWith("__Secure"),
  });
  if (!token) return null;
  if (token.dev) {
    return { "X-Tenant-Id": token.dev.tenantId, "X-User-Id": token.dev.subject, "X-Role": token.dev.role };
  }
  if (token.idToken && !token.error) return { Authorization: `Bearer ${token.idToken}` };
  return null;
}
