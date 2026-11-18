"use client";
import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Field, Input, Select, Textarea } from "@/components/ui/input";
import { bff } from "@/lib/client";
import { errorMessage } from "@/lib/errors";
import { validateAccount, validateActivity, validateContact, validateOpportunity } from "@/lib/validation";
import type { Stage } from "@/lib/types";

function useSubmit(onDone?: () => void) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  async function run(fn: () => Promise<unknown>, success: string) {
    setBusy(true);
    try {
      await fn();
      toast.success(success);
      onDone?.();
      router.refresh();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return { busy, run };
}

function Collapsible({ label, children, open, setOpen }: { label: string; children: React.ReactNode; open: boolean; setOpen: (b: boolean) => void }) {
  return (
    <div>
      <Button variant={open ? "ghost" : "default"} size="sm" onClick={() => setOpen(!open)} aria-expanded={open}>
        {open ? "Cancel" : label}
      </Button>
      {open ? <div className="mt-3 rounded-lg border border-border bg-muted/40 p-4">{children}</div> : null}
    </div>
  );
}

export function NewAccountForm() {
  const [open, setOpen] = useState(false);
  const [errors, setErrors] = useState<Record<string, string | undefined>>({});
  const { busy, run } = useSubmit(() => setOpen(false));
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const v = validateAccount({ name: String(f.get("name")), domain: String(f.get("domain")), employee_count: String(f.get("employees")) });
    if (!v.ok) return setErrors(v.errors);
    setErrors({});
    void run(() => bff("/accounts", { method: "POST", body: v.value }), "Account created");
  }
  return (
    <Collapsible label="New account" open={open} setOpen={setOpen}>
      <form onSubmit={submit} className="grid gap-3 sm:grid-cols-3" noValidate>
        <Field label="Name" error={errors.name} htmlFor="acc-name">
          <Input id="acc-name" name="name" placeholder="Acme Corp" />
        </Field>
        <Field label="Domain" error={errors.domain} htmlFor="acc-domain">
          <Input id="acc-domain" name="domain" placeholder="acme.com" />
        </Field>
        <Field label="Employees" error={errors.employee_count} htmlFor="acc-emp">
          <Input id="acc-emp" name="employees" inputMode="numeric" placeholder="120" />
        </Field>
        <div className="sm:col-span-3">
          <Button type="submit" disabled={busy}>
            Create account
          </Button>
        </div>
      </form>
    </Collapsible>
  );
}

export function NewContactForm({ accountId }: { accountId?: string }) {
  const [open, setOpen] = useState(false);
  const [errors, setErrors] = useState<Record<string, string | undefined>>({});
  const { busy, run } = useSubmit(() => setOpen(false));
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const v = validateContact({ first_name: String(f.get("first_name")), last_name: String(f.get("last_name")), email: String(f.get("email")) });
    if (!v.ok) return setErrors(v.errors);
    setErrors({});
    const title = String(f.get("title")).trim();
    void run(() => bff("/contacts", { method: "POST", body: { ...v.value, ...(title ? { title } : {}), ...(accountId ? { account_id: accountId } : {}) } }), "Contact created");
  }
  return (
    <Collapsible label="New contact" open={open} setOpen={setOpen}>
      <form onSubmit={submit} className="grid gap-3 sm:grid-cols-4" noValidate>
        <Field label="First name" error={errors.first_name} htmlFor="ct-first">
          <Input id="ct-first" name="first_name" />
        </Field>
        <Field label="Last name" htmlFor="ct-last">
          <Input id="ct-last" name="last_name" />
        </Field>
        <Field label="Email" error={errors.email} htmlFor="ct-email">
          <Input id="ct-email" name="email" type="email" />
        </Field>
        <Field label="Title" htmlFor="ct-title">
          <Input id="ct-title" name="title" />
        </Field>
        <div className="sm:col-span-4">
          <Button type="submit" disabled={busy}>
            Create contact
          </Button>
        </div>
      </form>
    </Collapsible>
  );
}

export function NewOpportunityForm({ accountId, stages }: { accountId: string; stages: Stage[] }) {
  const [open, setOpen] = useState(false);
  const [errors, setErrors] = useState<Record<string, string | undefined>>({});
  const { busy, run } = useSubmit(() => setOpen(false));
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const v = validateOpportunity({ name: String(f.get("name")), account_id: accountId, stage_id: String(f.get("stage_id")), amount: String(f.get("amount")) });
    if (!v.ok) return setErrors(v.errors);
    setErrors({});
    void run(() => bff("/opportunities", { method: "POST", body: v.value }), "Opportunity created");
  }
  return (
    <Collapsible label="New opportunity" open={open} setOpen={setOpen}>
      <form onSubmit={submit} className="grid gap-3 sm:grid-cols-3" noValidate>
        <Field label="Name" error={errors.name} htmlFor="op-name">
          <Input id="op-name" name="name" placeholder="Q4 expansion" />
        </Field>
        <Field label="Stage" error={errors.stage_id} htmlFor="op-stage">
          <Select id="op-stage" name="stage_id" defaultValue={stages[0]?.id}>
            {stages.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Amount (USD)" error={errors.amount} htmlFor="op-amount">
          <Input id="op-amount" name="amount" inputMode="decimal" placeholder="12,500" />
        </Field>
        <div className="sm:col-span-3">
          <Button type="submit" disabled={busy}>
            Create opportunity
          </Button>
        </div>
      </form>
    </Collapsible>
  );
}

/** Notes and call logs only; email sending arrives in Phase 2. */
export function ActivityComposer({ accountId, contactId, opportunityId }: { accountId: string; contactId?: string; opportunityId?: string }) {
  const [errors, setErrors] = useState<Record<string, string | undefined>>({});
  const { busy, run } = useSubmit();
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = e.currentTarget;
    const f = new FormData(form);
    const v = validateActivity({ type: String(f.get("type")), subject: String(f.get("subject")) });
    if (!v.ok) return setErrors(v.errors);
    setErrors({});
    void run(
      () =>
        bff("/activities", {
          method: "POST",
          body: { ...v.value, body: String(f.get("body")), account_id: accountId, ...(contactId ? { contact_id: contactId } : {}), ...(opportunityId ? { opportunity_id: opportunityId } : {}) },
        }).then(() => form.reset()),
      "Activity logged",
    );
  }
  return (
    <form onSubmit={submit} className="space-y-3 rounded-lg border border-border bg-muted/40 p-4" noValidate aria-label="Log activity">
      <div className="grid gap-3 sm:grid-cols-[140px_1fr]">
        <Field label="Type" error={errors.type} htmlFor="act-type">
          <Select id="act-type" name="type" defaultValue="note">
            <option value="note">Note</option>
            <option value="call">Call log</option>
          </Select>
        </Field>
        <Field label="Subject" error={errors.subject} htmlFor="act-subject">
          <Input id="act-subject" name="subject" placeholder="What happened?" />
        </Field>
      </div>
      <Field label="Details" htmlFor="act-body">
        <Textarea id="act-body" name="body" />
      </Field>
      <Button type="submit" disabled={busy}>
        Log activity
      </Button>
    </form>
  );
}

/** Stage selector used on the account page and the board's keyboard fallback. */
export function StageSelect({ opportunityId, stageId, stages, disabled }: { opportunityId: string; stageId: string; stages: Stage[]; disabled?: boolean }) {
  const { run } = useSubmit();
  return (
    <Select
      aria-label="Change stage"
      className="h-8 text-xs"
      value={stageId}
      disabled={disabled}
      onChange={(e) => void run(() => bff(`/opportunities/${opportunityId}`, { method: "PATCH", body: { stage_id: e.target.value } }), "Stage updated")}
    >
      {stages.map((s) => (
        <option key={s.id} value={s.id}>
          {s.name}
        </option>
      ))}
    </Select>
  );
}
