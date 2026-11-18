import Link from "next/link";
import { ActivityComposer, NewContactForm, NewOpportunityForm, StageSelect } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TD, TH, THead, TR } from "@/components/ui/table";
import { apiGet } from "@/lib/api";
import { formatCents, formatDate, formatDateTime } from "@/lib/format";
import { ownerNames } from "@/lib/owners";
import { canWrite } from "@/lib/roles";
import type { Account, Activity, Contact, Opportunity, Page, Pipeline, Whoami } from "@/lib/types";

const TABS = [
  ["overview", "Overview"],
  ["contacts", "Contacts"],
  ["opportunities", "Opportunities"],
  ["activity", "Activity"],
] as const;

export default async function AccountDetail({ params, searchParams }: { params: Promise<{ id: string }>; searchParams: Promise<{ tab?: string }> }) {
  const { id } = await params;
  const tab = (await searchParams).tab ?? "overview";
  const me = await apiGet<Whoami>("/api/whoami");
  // A 404 here means "not found OR not yours": the API never reveals which.
  const account = await apiGet<Account>(`/api/v1/accounts/${id}`);
  const [contacts, opps, acts, pipelines, owners] = await Promise.all([
    apiGet<Page<Contact>>(`/api/v1/contacts?account_id=${id}&limit=100`),
    apiGet<Page<Opportunity>>(`/api/v1/opportunities?account_id=${id}&limit=100`),
    apiGet<Page<Activity>>(`/api/v1/activities?account_id=${id}&limit=50`),
    apiGet<Page<Pipeline>>("/api/v1/pipelines"),
    ownerNames(me),
  ]);
  const stages = (pipelines.items.find((p) => p.is_default) ?? pipelines.items[0])?.stages ?? [];
  const stageName = Object.fromEntries(pipelines.items.flatMap((p) => p.stages.map((s) => [s.id, s.name])));
  const write = canWrite(me.role);

  return (
    <div className="space-y-5">
      <div>
        <p className="text-xs text-muted-foreground">
          <Link href="/accounts" className="hover:underline">
            Accounts
          </Link>{" "}
          /
        </p>
        <h1 className="text-xl font-semibold" data-testid="account-name">
          {account.name}
        </h1>
        <p className="text-sm text-muted-foreground">
          {account.domain ?? "no domain"} · {account.industry ?? "no industry"} · owner {account.owner_id ? (owners[account.owner_id] ?? "a teammate") : "unassigned"}
        </p>
      </div>

      <nav className="flex gap-1 border-b border-border" aria-label="Account sections">
        {TABS.map(([key, label]) => (
          <Link
            key={key}
            href={`/accounts/${id}?tab=${key}`}
            aria-current={tab === key ? "page" : undefined}
            className={`-mb-px border-b-2 px-3 py-2 text-sm ${tab === key ? "border-primary font-medium text-primary" : "border-transparent text-muted-foreground hover:text-foreground"}`}
          >
            {label}
            {key === "contacts" ? ` (${contacts.items.length})` : key === "opportunities" ? ` (${opps.items.length})` : ""}
          </Link>
        ))}
      </nav>

      {tab === "overview" ? (
        <Card>
          <CardContent>
            <dl className="grid gap-4 text-sm sm:grid-cols-3">
              {[
                ["Website", account.website],
                ["Employees", account.employee_count?.toString()],
                ["Last contact", account.last_contact_at ? formatDateTime(account.last_contact_at) : null],
                ["Created", formatDate(account.created_at)],
              ].map(([k, v]) => (
                <div key={k as string}>
                  <dt className="text-xs uppercase text-muted-foreground">{k}</dt>
                  <dd>{v ?? "—"}</dd>
                </div>
              ))}
            </dl>
          </CardContent>
        </Card>
      ) : null}

      {tab === "contacts" ? (
        <div className="space-y-3">
          {write ? <NewContactForm accountId={id} /> : null}
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
                {contacts.items.map((c) => (
                  <TR key={c.id}>
                    <TD>
                      <Link href={`/contacts/${c.id}`} className="font-medium text-primary hover:underline">
                        {`${c.first_name} ${c.last_name}`.trim() || c.email}
                      </Link>
                    </TD>
                    <TD>{c.email ?? "—"}</TD>
                    <TD>{c.title ?? "—"}</TD>
                    <TD>
                      <Badge tone={c.consent_status === "granted" ? "success" : c.consent_status === "revoked" ? "danger" : "neutral"}>{c.consent_status}</Badge>
                    </TD>
                  </TR>
                ))}
                {contacts.items.length === 0 ? (
                  <TR>
                    <TD colSpan={4} className="py-6 text-center text-muted-foreground">
                      No contacts yet.
                    </TD>
                  </TR>
                ) : null}
              </tbody>
            </Table>
          </Card>
        </div>
      ) : null}

      {tab === "opportunities" ? (
        <div className="space-y-3">
          {write ? <NewOpportunityForm accountId={id} stages={stages} /> : null}
          <Card>
            <Table aria-label="Opportunities">
              <THead>
                <TR>
                  <TH>Name</TH>
                  <TH>Amount</TH>
                  <TH>Stage</TH>
                  <TH>Status</TH>
                </TR>
              </THead>
              <tbody>
                {opps.items.map((o) => (
                  <TR key={o.id} data-opportunity={o.name}>
                    <TD className="font-medium">{o.name}</TD>
                    <TD>{formatCents(o.amount_cents, o.currency)}</TD>
                    <TD className="w-48">
                      {write ? <StageSelect opportunityId={o.id} stageId={o.stage_id} stages={stages} /> : (stageName[o.stage_id] ?? "—")}
                    </TD>
                    <TD>
                      <Badge tone={o.status === "won" ? "success" : o.status === "lost" ? "danger" : "neutral"}>{o.status}</Badge>
                    </TD>
                  </TR>
                ))}
                {opps.items.length === 0 ? (
                  <TR>
                    <TD colSpan={4} className="py-6 text-center text-muted-foreground">
                      No opportunities yet.
                    </TD>
                  </TR>
                ) : null}
              </tbody>
            </Table>
          </Card>
        </div>
      ) : null}

      {tab === "activity" ? (
        <div className="space-y-4">
          {write ? <ActivityComposer accountId={id} /> : null}
          <Card>
            <CardHeader>
              <CardTitle>Timeline</CardTitle>
            </CardHeader>
            <CardContent>
              <ol className="space-y-4" aria-label="Activity timeline">
                {acts.items.map((a) => (
                  <li key={a.id} className="text-sm" data-testid="timeline-item">
                    <div className="flex items-center gap-2">
                      <Badge>{a.type}</Badge>
                      <span className="font-medium">{a.subject || "(no subject)"}</span>
                    </div>
                    {a.body ? <p className="mt-1 whitespace-pre-wrap text-muted-foreground">{a.body}</p> : null}
                    <p className="text-xs text-muted-foreground">
                      {formatDateTime(a.occurred_at)} · {owners[a.created_by] ?? "a teammate"}
                    </p>
                  </li>
                ))}
                {acts.items.length === 0 ? <li className="text-sm text-muted-foreground">No activity logged yet.</li> : null}
              </ol>
            </CardContent>
          </Card>
        </div>
      ) : null}
    </div>
  );
}
