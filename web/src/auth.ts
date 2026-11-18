import NextAuth from "next-auth";
import Credentials from "next-auth/providers/credentials";
import type { NextAuthConfig } from "next-auth";
import { findPersona } from "@/lib/personas";

const issuer = process.env.OIDC_ISSUER ?? "";
const clientId = process.env.OIDC_CLIENT_ID ?? "";
const clientSecret = process.env.OIDC_CLIENT_SECRET ?? "";

// Dev login mirrors the API's dev mode: only when no OIDC issuer is
// configured AND APP_ENV is not production. Production without an issuer is
// a configuration error, never an implicit dev login.
export const devMode = !issuer && process.env.APP_ENV !== "production";
if (!issuer && process.env.APP_ENV === "production") {
  throw new Error("OIDC_ISSUER is required when APP_ENV=production");
}

const ROLE_ORDER = ["admin", "manager", "finance", "legal", "rep"];

function pickRole(roles: unknown): string | undefined {
  if (!Array.isArray(roles)) return undefined;
  return ROLE_ORDER.find((r) => roles.includes(r));
}

function jwtPayload(token?: string): Record<string, unknown> {
  if (!token) return {};
  try {
    return JSON.parse(Buffer.from(token.split(".")[1], "base64url").toString("utf8"));
  } catch {
    return {};
  }
}

async function refreshIdToken(refreshToken: string) {
  const disco = await (await fetch(`${issuer}/.well-known/openid-configuration`)).json();
  const res = await fetch(disco.token_endpoint, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "refresh_token",
      refresh_token: refreshToken,
      client_id: clientId,
      client_secret: clientSecret,
    }),
  });
  const body = await res.json();
  if (!res.ok || !body.id_token) throw new Error("refresh failed");
  return body as { id_token: string; refresh_token?: string; expires_in?: number };
}

const providers: NextAuthConfig["providers"] = devMode
  ? [
      Credentials({
        id: "dev",
        name: "Demo login",
        credentials: { persona: {} },
        authorize: async (c) => {
          const p = findPersona(String(c?.persona ?? ""));
          if (!p) return null;
          return { id: p.id, name: p.label, devTenantId: p.tenantId, devSubject: p.subject, devRole: p.role };
        },
      }),
    ]
  : [
      {
        id: "ferriskey",
        name: "FerrisKey",
        type: "oidc",
        issuer,
        clientId,
        clientSecret,
        checks: ["pkce", "state"],
        authorization: { params: { scope: "openid profile email" } },
      },
    ];

export const { handlers, auth, signIn, signOut } = NextAuth({
  providers,
  trustHost: true,
  session: { strategy: "jwt", maxAge: 8 * 3600 },
  pages: { signIn: "/login" },
  callbacks: {
    async jwt({ token, user, account }) {
      if (user && devMode) {
        // Server-side only: the tenant and subject live in the encrypted
        // session cookie and are never accepted from the browser.
        token.dev = { tenantId: user.devTenantId!, subject: user.devSubject!, role: user.devRole! };
        token.role = user.devRole;
        return token;
      }
      if (account?.id_token) {
        token.idToken = account.id_token;
        token.refreshToken = account.refresh_token;
        token.expiresAt = account.expires_at;
        token.role = pickRole(jwtPayload(account.id_token).roles);
        return token;
      }
      if (!devMode && token.expiresAt && Date.now() / 1000 > token.expiresAt - 30 && token.refreshToken) {
        try {
          const r = await refreshIdToken(token.refreshToken);
          token.idToken = r.id_token;
          token.refreshToken = r.refresh_token ?? token.refreshToken;
          token.expiresAt = Math.floor(Date.now() / 1000) + (r.expires_in ?? 300);
          token.role = pickRole(jwtPayload(r.id_token).roles) ?? token.role;
          delete token.error;
        } catch {
          token.error = "RefreshFailed";
        }
      }
      return token;
    },
    async session({ session, token }) {
      // The session object is readable by the browser: expose ONLY name and
      // role (display), never tenant ids, tokens or subjects.
      session.user = { name: token.name ?? null, role: token.role } as typeof session.user;
      if (token.error) session.error = token.error;
      return session;
    },
  },
});
