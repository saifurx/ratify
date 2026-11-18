"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { bff } from "@/lib/client";
import { errorMessage } from "@/lib/errors";
import { reasonText } from "@/lib/format";

/** Convert is disabled, with a visible explanation, when consent.CanContact is false. */
export function ConvertLeadButton({ leadId, allowed, reason, canWrite, converted }: { leadId: string; allowed: boolean; reason: string; canWrite: boolean; converted: boolean }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  if (converted) return <span className="text-xs text-muted-foreground">Converted</span>;
  const blocked = !allowed || !canWrite;
  const why = !canWrite ? "Your role cannot convert leads." : reasonText(reason);
  async function convert() {
    setBusy(true);
    try {
      const r = await bff<{ account_id: string; opportunity_id: string }>(`/leads/${leadId}/convert`, { method: "POST", body: {} });
      toast.success("Lead converted to an account, contact and opportunity");
      router.push(`/accounts/${r.account_id}`);
      router.refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-1">
      <Button size="sm" disabled={blocked || busy} onClick={convert} title={blocked ? why : "Convert to account, contact and opportunity"} aria-describedby={blocked ? `why-${leadId}` : undefined}>
        Convert
      </Button>
      {blocked ? (
        <p id={`why-${leadId}`} className="max-w-[16rem] text-xs text-muted-foreground" data-testid="convert-reason">
          {why}
        </p>
      ) : null}
      <span className="hidden">
        <Link href="/leads">leads</Link>
      </span>
    </div>
  );
}
