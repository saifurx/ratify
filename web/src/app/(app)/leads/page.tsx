import { ConvertLeadButton } from "@/components/convert-lead-button";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Table, TD, TH, THead, TR } from "@/components/ui/table";
import { apiGet } from "@/lib/api";
import { formatDate } from "@/lib/format";
import { canWrite } from "@/lib/roles";
import type { Lead, Page, Whoami } from "@/lib/types";

export default async function LeadsPage({ searchParams }: { searchParams: Promise<{ cursor?: string }> }) {
  const { cursor } = await searchParams;
  const me = await apiGet<Whoami>("/api/whoami");
  const page = await apiGet<Page<Lead>>(`/api/v1/leads?limit=25${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`);
  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold">Lead inbox</h1>
        <p className="text-sm text-muted-foreground">Inbound leads from hosted forms. Scores are produced by a model trained on synthetic data until real history exists.</p>
      </div>
      <Card>
        <Table aria-label="Leads">
          <THead>
            <TR>
              <TH>Lead</TH>
              <TH>Source</TH>
              <TH>Score</TH>
              <TH>Consent</TH>
              <TH>Captured</TH>
              <TH>Action</TH>
            </TR>
          </THead>
          <tbody>
            {page.items.map((l) => (
              <TR key={l.id} data-lead={l.email}>
                <TD>
                  <div className="font-medium">{`${l.first_name} ${l.last_name}`.trim() || l.email}</div>
                  <div className="text-xs text-muted-foreground">
                    {l.email}
                    {l.company ? ` · ${l.company}` : ""}
                  </div>
                  {l.possible_duplicate_of ? <Badge tone="warning">possible duplicate</Badge> : null}
                </TD>
                <TD>{l.source}</TD>
                <TD>
                  {l.score ? (
                    <div className="flex items-center gap-2" data-testid="score">
                      <span className="text-base font-semibold tabular-nums">{l.score.score}</span>
                      {l.score.preliminary ? <Badge tone="warning">preliminary</Badge> : null}
                    </div>
                  ) : (
                    <span className="text-xs text-muted-foreground">Scoring…</span>
                  )}
                </TD>
                <TD>
                  <Badge tone={l.consent_status === "granted" ? "success" : l.consent_status === "revoked" ? "danger" : "neutral"}>{l.consent_status}</Badge>
                </TD>
                <TD>{formatDate(l.created_at)}</TD>
                <TD>
                  <ConvertLeadButton
                    leadId={l.id}
                    allowed={!!l.can_contact?.allowed}
                    reason={l.can_contact?.reason ?? "unknown"}
                    canWrite={canWrite(me.role)}
                    converted={!!l.converted_at}
                  />
                </TD>
              </TR>
            ))}
            {page.items.length === 0 ? (
              <TR>
                <TD colSpan={6} className="py-8 text-center text-muted-foreground">
                  No leads yet.
                </TD>
              </TR>
            ) : null}
          </tbody>
        </Table>
      </Card>
    </div>
  );
}
