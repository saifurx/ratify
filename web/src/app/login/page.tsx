import { redirect } from "next/navigation";
import { auth, devMode, signIn } from "@/auth";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { PERSONAS } from "@/lib/personas";

export const dynamic = "force-dynamic";

export default async function LoginPage() {
  const session = await auth();
  if (session && !session.error) redirect("/");

  return (
    <main className="mx-auto max-w-3xl px-4 py-16">
      <h1 className="text-2xl font-semibold">Ratify</h1>
      <p className="mt-1 text-sm text-muted-foreground">Sign in to continue.</p>

      {devMode ? (
        <>
          <p className="mt-6 rounded-md border border-warning/30 bg-warning/5 px-3 py-2 text-sm text-warning" role="note">
            Demo login (development only): pick a seeded user. This screen does not exist when an OIDC issuer is configured or APP_ENV=production.
          </p>
          <div className="mt-4 grid gap-4 sm:grid-cols-2">
            {[...new Set(PERSONAS.map((p) => p.tenantLabel))].map((tenant) => (
              <Card key={tenant}>
                <CardHeader>
                  <CardTitle>{tenant}</CardTitle>
                </CardHeader>
                <CardContent className="space-y-1.5">
                  {PERSONAS.filter((p) => p.tenantLabel === tenant).map((p) => (
                    <form
                      key={p.id}
                      action={async () => {
                        "use server";
                        await signIn("dev", { persona: p.id, redirectTo: "/" });
                      }}
                    >
                      <Button type="submit" variant="outline" className="w-full justify-between" data-persona={p.id}>
                        <span>{p.label}</span>
                        <span className="text-xs text-muted-foreground">{p.role}</span>
                      </Button>
                    </form>
                  ))}
                </CardContent>
              </Card>
            ))}
          </div>
        </>
      ) : (
        <form
          className="mt-8"
          action={async () => {
            "use server";
            await signIn("ferriskey", { redirectTo: "/" });
          }}
        >
          <Button type="submit">Sign in with FerrisKey</Button>
          {session?.error ? <p className="mt-3 text-sm text-destructive">Your session expired. Please sign in again.</p> : null}
        </form>
      )}
    </main>
  );
}
