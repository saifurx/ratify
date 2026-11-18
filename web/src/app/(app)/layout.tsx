import { BarChart3, Building2, Inbox, KanbanSquare, LogOut, Settings, Users } from "lucide-react";
import Link from "next/link";
import { auth, signOut } from "@/auth";
import { SearchBar } from "@/components/search-bar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { apiGet } from "@/lib/api";
import { isAdmin, scopeLabel } from "@/lib/roles";
import type { Whoami } from "@/lib/types";

export const dynamic = "force-dynamic";

export default async function AppLayout({ children }: { children: React.ReactNode }) {
  const [session, me] = await Promise.all([auth(), apiGet<Whoami>("/api/whoami")]);
  const nav = [
    { href: "/", label: "Dashboard", icon: BarChart3 },
    { href: "/accounts", label: "Accounts", icon: Building2 },
    { href: "/contacts", label: "Contacts", icon: Users },
    { href: "/opportunities", label: "Pipeline", icon: KanbanSquare },
    { href: "/leads", label: "Lead inbox", icon: Inbox },
    ...(isAdmin(me.role) ? [{ href: "/settings", label: "Settings", icon: Settings }] : []),
  ];
  return (
    <div className="flex min-h-screen">
      <aside className="hidden w-56 shrink-0 flex-col border-r border-border bg-white md:flex">
        <div className="px-5 py-4 text-lg font-semibold">Ratify</div>
        <nav className="flex-1 space-y-0.5 px-2" aria-label="Main">
          {nav.map(({ href, label, icon: Icon }) => (
            <Link key={href} href={href} className="flex items-center gap-2.5 rounded-md px-3 py-2 text-sm hover:bg-muted">
              <Icon className="h-4 w-4 text-muted-foreground" aria-hidden />
              {label}
            </Link>
          ))}
        </nav>
        <div className="border-t border-border p-4 text-xs text-muted-foreground">
          <p className="font-medium text-foreground" data-testid="user-name">
            {session?.user?.name}
          </p>
          <p className="mt-1">
            <Badge tone="info" data-testid="user-role">
              {me.role}
            </Badge>
          </p>
          <p className="mt-1">{scopeLabel(me.role)}</p>
        </div>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center justify-between gap-4 border-b border-border bg-white px-6 py-3">
          <SearchBar />
          <form
            action={async () => {
              "use server";
              await signOut({ redirectTo: "/login" });
            }}
          >
            <Button type="submit" variant="ghost" size="sm" aria-label="Sign out">
              <LogOut className="h-4 w-4" aria-hidden /> Sign out
            </Button>
          </form>
        </header>
        <main className="flex-1 px-6 py-6">{children}</main>
      </div>
    </div>
  );
}
