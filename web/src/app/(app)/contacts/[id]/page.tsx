import Link from "next/link";
import { ActivityComposer } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { apiGet } from "@/lib/api";
import { formatDate, formatDateTime } from "@/lib/format";
import { canWrite } from "@/lib/roles";
import type { Account, Activity, Contact, Page, Whoami } from "@/lib/types";

export default async function ContactDetail({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const me = await apiGet<Whoami>("/api/whoami");
  const c = await apiGet<Contact>(`/api/v1/contacts/${id}`);
  const [acts, account] = await Promise.all([
    apiGet<Page<Activity>>(`/api/v1/activities?contact_id=${id}&limit=50`),
    c.account_id ? apiGet<Account>(`/api/v1/accounts/${c.account_id}`).catch(() => null) : Promise.resolve(null),
  ]);
  const name = `${c.first_name} ${c.last_name}`.trim() || c.email || "Contact";
  return (
    <div className="space-y-5">
      <div>
        <p className="text-xs text-muted-foreground">
          <Link href="/contacts" className="hover:underline">
            Contacts
          </Link>{" "}
          /
        </p>
        <h1 className="text-xl font-semibold" data-testid="contact-name">
          {name}
        </h1>
        <p className="text-sm text-muted-foreground">
          {c.title ?? "no title"} · {account ? <Link href={`/accounts/${account.id}`} className="text-primary hover:underline">{account.name}</Link> : "no account"}
        </p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Contact details and consent</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid gap-4 text-sm sm:grid-cols-3">
            <div><dt className="text-xs uppercase text-muted-foreground">Email</dt><dd>{c.email ?? "—"}</dd></div>
            <div><dt className="text-xs uppercase text-muted-foreground">Phone</dt><dd>{c.phone ?? "—"}</dd></div>
            <div>
              <dt className="text-xs uppercase text-muted-foreground">Consent</dt>
              <dd className="flex items-center gap-2">
                <Badge tone={c.consent_status === "granted" ? "success" : c.consent_status === "revoked" ? "danger" : "neutral"}>{c.consent_status}</Badge>
                {c.do_not_contact ? <Badge tone="danger">do not contact</Badge> : null}
              </dd>
            </div>
            <div><dt className="text-xs uppercase text-muted-foreground">Lawful basis</dt><dd>{c.lawful_basis ?? "—"}</dd></div>
            <div><dt className="text-xs uppercase text-muted-foreground">Source</dt><dd>{c.consent_source ?? "—"}</dd></div>
            <div><dt className="text-xs uppercase text-muted-foreground">Captured</dt><dd>{formatDate(c.consent_captured_at)}</dd></div>
          </dl>
        </CardContent>
      </Card>
      {canWrite(me.role) && c.account_id ? <ActivityComposer accountId={c.account_id} contactId={id} /> : null}
      <Card>
        <CardHeader>
          <CardTitle>Activity</CardTitle>
        </CardHeader>
        <CardContent>
          <ol className="space-y-4" aria-label="Contact activity">
            {acts.items.map((a) => (
              <li key={a.id} className="text-sm">
                <div className="flex items-center gap-2"><Badge>{a.type}</Badge><span className="font-medium">{a.subject || "(no subject)"}</span></div>
                {a.body ? <p className="mt-1 whitespace-pre-wrap text-muted-foreground">{a.body}</p> : null}
                <p className="text-xs text-muted-foreground">{formatDateTime(a.occurred_at)}</p>
              </li>
            ))}
            {acts.items.length === 0 ? <li className="text-sm text-muted-foreground">No activity for this contact yet.</li> : null}
          </ol>
        </CardContent>
      </Card>
    </div>
  );
}
