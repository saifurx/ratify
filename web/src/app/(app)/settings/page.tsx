import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { apiGet } from "@/lib/api";
import { isAdmin } from "@/lib/roles";
import type { Whoami } from "@/lib/types";

export default async function SettingsPage() {
  const me = await apiGet<Whoami>("/api/whoami");
  if (!isAdmin(me.role)) {
    return (
      <div className="mx-auto max-w-md py-16 text-center" role="alert">
        <h1 className="text-lg font-semibold">Admins only</h1>
        <p className="mt-2 text-sm text-muted-foreground">Settings are available to organisation admins.</p>
      </div>
    );
  }
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">Settings</h1>
      <Card>
        <CardHeader>
          <CardTitle>Custom fields</CardTitle>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">Custom field definitions will be configured here (placeholder for a later sprint).</CardContent>
      </Card>
    </div>
  );
}
