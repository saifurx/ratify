"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { StageSelect } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { bff } from "@/lib/client";
import { errorMessage } from "@/lib/errors";
import { formatCents } from "@/lib/format";
import type { Opportunity, Stage } from "@/lib/types";

/** Pipeline board. Dragging a card PATCHes the opportunity's stage (the API
 *  emits opportunity.stage_changed and logs a timeline activity). Each card
 *  also has a stage <select> as a keyboard / touch fallback. */
export function Board({ stages, initial, accountNames, canWrite }: { stages: Stage[]; initial: Opportunity[]; accountNames: Record<string, string>; canWrite: boolean }) {
  const router = useRouter();
  const [opps, setOpps] = useState(initial);
  const [dragging, setDragging] = useState<string | null>(null);
  const [over, setOver] = useState<string | null>(null);

  async function move(id: string, stageId: string) {
    const prev = opps;
    const cur = prev.find((o) => o.id === id);
    if (!cur || cur.stage_id === stageId) return;
    setOpps(prev.map((o) => (o.id === id ? { ...o, stage_id: stageId } : o))); // optimistic
    try {
      const updated = await bff<Opportunity>(`/opportunities/${id}`, { method: "PATCH", body: { stage_id: stageId } });
      setOpps((cur) => cur.map((o) => (o.id === id ? updated : o)));
      toast.success(`Moved to ${stages.find((s) => s.id === stageId)?.name}`);
      router.refresh();
    } catch (e) {
      setOpps(prev); // revert
      toast.error(errorMessage(e));
    }
  }

  return (
    <div className="flex gap-3 overflow-x-auto pb-4" data-testid="board">
      {stages.map((st) => {
        const col = opps.filter((o) => o.stage_id === st.id);
        const total = col.reduce((n, o) => n + o.amount_cents, 0);
        return (
          <section
            key={st.id}
            aria-label={`Stage ${st.name}`}
            data-stage={st.name}
            onDragOver={(e) => {
              if (canWrite) {
                e.preventDefault();
                setOver(st.id);
              }
            }}
            onDragLeave={() => setOver((o) => (o === st.id ? null : o))}
            onDrop={(e) => {
              e.preventDefault();
              setOver(null);
              if (dragging) void move(dragging, st.id);
              setDragging(null);
            }}
            className={`w-64 shrink-0 rounded-lg border bg-muted/40 ${over === st.id ? "border-primary bg-primary/5" : "border-border"}`}
          >
            <header className="border-b border-border px-3 py-2">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold">{st.name}</h3>
                <Badge>{col.length}</Badge>
              </div>
              <p className="text-xs text-muted-foreground">{formatCents(total)}</p>
            </header>
            <ul className="space-y-2 p-2">
              {col.map((o) => (
                <li
                  key={o.id}
                  draggable={canWrite}
                  data-opportunity={o.name}
                  onDragStart={() => setDragging(o.id)}
                  onDragEnd={() => setDragging(null)}
                  className={`rounded-md border border-border bg-white p-2.5 shadow-sm ${canWrite ? "cursor-grab" : ""}`}
                >
                  <Link href={`/accounts/${o.account_id}`} className="block text-sm font-medium hover:underline">
                    {o.name}
                  </Link>
                  <p className="truncate text-xs text-muted-foreground">{accountNames[o.account_id] ?? "Account"}</p>
                  <div className="mt-1.5 flex items-center justify-between">
                    <span className="text-sm">{formatCents(o.amount_cents, o.currency)}</span>
                    <Badge tone={o.status === "won" ? "success" : o.status === "lost" ? "danger" : "neutral"}>{o.status}</Badge>
                  </div>
                  {canWrite ? (
                    <div className="mt-2">
                      <StageSelect opportunityId={o.id} stageId={o.stage_id} stages={stages} />
                    </div>
                  ) : null}
                </li>
              ))}
              {col.length === 0 ? <li className="px-1 py-3 text-center text-xs text-muted-foreground">No deals</li> : null}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
