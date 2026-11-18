import Link from "next/link";
import { NewAccountForm } from "@/components/forms";
import { Card } from "@/components/ui/card";
import { Table, TD, TH, THead, TR } from "@/components/ui/table";
import { apiGet } from "@/lib/api";
import { formatDate } from "@/lib/format";
import { ownerNames } from "@/lib/owners";
import { canWrite } from "@/lib/roles";
import type { Account, Page, Whoami } from "@/lib/types";

export default async function AccountsPage({ searchParams }: { searchParams: Promise<{ cursor?: string }> }) {
  const { cursor } = await searchParams;
  const me = await apiGet<Whoami>("/api/whoami");
  const [page, owners] = await Promise.all([
    apiGet<Page<Account>>(`/api/v1/accounts?limit=25${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`),
    ownerNames(me),
  ]);
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Accounts</h1>
        {canWrite(me.role) ? <NewAccountForm /> : null}
      </div>
      <Card>
        <Table aria-label="Accounts">
          <THead>
            <TR>
              <TH>Name</TH>
              <TH>Domain</TH>
              <TH>Industry</TH>
              <TH>Owner</TH>
              <TH>Created</TH>
            </TR>
          </THead>
          <tbody>
            {page.items.map((a) => (
              <TR key={a.id}>
                <TD>
                  <Link href={`/accounts/${a.id}`} className="font-medium text-primary hover:underline">
                    {a.name}
                  </Link>
                </TD>
                <TD>{a.domain ?? "—"}</TD>
                <TD>{a.industry ?? "—"}</TD>
                <TD>{a.owner_id ? (owners[a.owner_id] ?? "Teammate") : "Unassigned"}</TD>
                <TD>{formatDate(a.created_at)}</TD>
              </TR>
            ))}
            {page.items.length === 0 ? (
              <TR>
                <TD colSpan={5} className="py-8 text-center text-muted-foreground">
                  No accounts yet.
                </TD>
              </TR>
            ) : null}
          </tbody>
        </Table>
      </Card>
      {page.next_cursor ? (
        <Link href={`/accounts?cursor=${encodeURIComponent(page.next_cursor)}`} className="text-sm text-primary hover:underline">
          Next page →
        </Link>
      ) : null}
    </div>
  );
}
