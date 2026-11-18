import "next-auth";

declare module "next-auth" {
  interface Session {
    user: { name?: string | null; role?: string };
    error?: string;
  }
  interface User {
    devTenantId?: string;
    devSubject?: string;
    devRole?: string;
  }
}

declare module "next-auth/jwt" {
  interface JWT {
    role?: string;
    dev?: { tenantId: string; subject: string; role: string };
    idToken?: string;
    refreshToken?: string;
    expiresAt?: number;
    error?: string;
  }
}
