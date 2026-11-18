import Link from "next/link";
import { NewContactForm } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Table, TD, TH, THead, TR } from "@/components/ui/table";
import { apiGet } from "@/lib/api";
import { canWrite } from "@/lib/roles";
import type { Contact, Page, Whoami } from "@/lib/types";

export default async function ContactsPage({ searchParams }: { searchParams: Promise<{ cursor?: string }> }) {
  const { cursor } = await searchParams;
  const me = await apiGet<Whoami>("/api/whoami");
  const page = await apiGet<Page<Contact>>(`/api/v1/contacts?limit=25${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`);
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Contacts</h1>
        {canWrite(me.role) ? <NewContactForm /> : null}
      </div>
      <Card>
        <Table aria-label="Contacts">
          <THead>
            <TR>
              <TH>Name</TH>
              <TH>Email</TH>
              <TH>Title</TH>
              <TH>Consent</TH>
            </TR>
          </THead>
          <tbody>
            {page.items.map((c) => (
              <TR key={c.id}>
                <TD>
                  <Link href={`/contacts/${c.id}`} className="font-medium text-primary hover:underline">
                    {`${c.first_name} ${c.last_name}`.trim() || c.email}
                  </Link>
                </TD>
                <TD>{c.email ?? "—"}</TD>
                <TD>{c.title ?? "—"}</TD>
                <TD>
                  {c.do_not_contact ? <Badge tone="danger">do not contact</Badge> : <Badge tone={c.consent_status === "granted" ? "success" : c.consent_status === "revoked" ? "danger" : "neutral"}>{c.consent_status}</Badge>}
                </TD>
              </TR>
            ))}
            {page.items.length === 0 ? (
              <TR>
                <TD colSpan={4} className="py-8 text-center text-muted-foreground">
                  No contacts yet.
                </TD>
              </TR>
            ) : null}
          </tbody>
        </Table>
      </Card>
      {page.next_cursor ? (
        <Link href={`/contacts?cursor=${encodeURIComponent(page.next_cursor)}`} className="text-sm text-primary hover:underline">
          Next page →
        </Link>
      ) : null}
    </div>
  );
}
