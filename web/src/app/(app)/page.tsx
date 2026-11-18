import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TD, TH, THead, TR } from "@/components/ui/table";
import { apiGet } from "@/lib/api";
import { formatCents, formatDate, formatDateTime } from "@/lib/format";
import type { Account, Dashboard, Page } from "@/lib/types";

export default async function DashboardPage() {
  const d = await apiGet<Dashboard>("/api/v1/dashboard");
  const maxValue = Math.max(1, ...d.stages.map((s) => s.value_cents));
  const accountIds = [...new Set(d.recent_activities.map((a) => a.account_id))];
  const names: Record<string, string> = {};
  if (accountIds.length) {
    const accts = await apiGet<Page<Account>>("/api/v1/accounts?limit=200");
    for (const a of accts.items) names[a.id] = a.name;
  }
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold">Dashboard</h1>
        <p className="text-sm text-muted-foreground">
          {d.open_count} open deals worth {formatCents(d.open_value_cents)} in {d.pipeline_name || "your pipeline"}.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Pipeline value by stage</CardTitle>
        </CardHeader>
        <CardContent>
          <ul className="space-y-2.5" data-testid="pipeline-by-stage">
            {d.stages.map((s) => (
              <li key={s.stage_id} className="grid grid-cols-[140px_1fr_110px] items-center gap-3 text-sm">
                <span>{s.name}</span>
                <div className="h-3 rounded bg-muted">
                  <div className="h-3 rounded bg-primary" style={{ width: `${Math.round((s.value_cents / maxValue) * 100)}%` }} />
                </div>
                <span className="text-right tabular-nums">
                  {formatCents(s.value_cents)} <span className="text-xs text-muted-foreground">({s.count})</span>
                </span>
              </li>
            ))}
            {d.stages.length === 0 ? <li className="text-sm text-muted-foreground">No pipeline is configured yet.</li> : null}
          </ul>
        </CardContent>
      </Card>

      <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>My open deals</CardTitle>
          </CardHeader>
          <Table>
            <THead>
              <TR>
                <TH>Deal</TH>
                <TH>Amount</TH>
                <TH>Close</TH>
              </TR>
            </THead>
            <tbody>
              {d.my_open_deals.map((o) => (
                <TR key={o.id}>
                  <TD>
                    <Link href={`/accounts/${o.account_id}`} className="hover:underline">
                      {o.name}
                    </Link>
                  </TD>
                  <TD>{formatCents(o.amount_cents, o.currency)}</TD>
                  <TD>{formatDate(o.close_date)}</TD>
                </TR>
              ))}
              {d.my_open_deals.length === 0 ? (
                <TR>
                  <TD colSpan={3} className="text-muted-foreground">
                    No open deals assigned to you.
                  </TD>
                </TR>
              ) : null}
            </tbody>
          </Table>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Recent activity</CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="space-y-3">
              {d.recent_activities.map((a) => (
                <li key={a.id} className="text-sm">
                  <div className="flex items-center gap-2">
                    <Badge>{a.type}</Badge>
                    <Link href={`/accounts/${a.account_id}`} className="font-medium hover:underline">
                      {a.subject || "(no subject)"}
                    </Link>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {names[a.account_id] ?? "Account"} · {formatDateTime(a.occurred_at)}
                  </p>
                </li>
              ))}
              {d.recent_activities.length === 0 ? <li className="text-sm text-muted-foreground">No activity yet.</li> : null}
            </ul>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
