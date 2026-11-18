export type FieldErrors<T extends string = string> = Partial<Record<T, string>>;
export type Result<V, F extends string> = { ok: true; value: V } | { ok: false; errors: FieldErrors<F> };

const EMAIL = /^[^@\s]+@[^@\s]+\.[^@\s]+$/;

export function validateAccount(i: { name: string; domain?: string; employee_count?: string }): Result<
  { name: string; domain?: string; employee_count?: number },
  "name" | "domain" | "employee_count"
> {
  const errors: FieldErrors<"name" | "domain" | "employee_count"> = {};
  const name = i.name.trim();
  if (!name) errors.name = "Name is required";
  else if (name.length > 200) errors.name = "Name must be at most 200 characters";
  const domain = (i.domain ?? "").trim();
  if (domain && !/^[a-z0-9.-]+\.[a-z]{2,}$/i.test(domain)) errors.domain = "Enter a domain like acme.com";
  let employees: number | undefined;
  if ((i.employee_count ?? "").trim() !== "") {
    employees = Number(i.employee_count);
    if (!Number.isInteger(employees) || employees < 0) errors.employee_count = "Employees must be a whole number, 0 or more";
  }
  if (Object.keys(errors).length) return { ok: false, errors };
  return { ok: true, value: { name, ...(domain ? { domain } : {}), ...(employees !== undefined ? { employee_count: employees } : {}) } };
}

export function validateContact(i: { first_name: string; last_name: string; email: string }): Result<
  { first_name: string; last_name: string; email?: string },
  "email" | "first_name"
> {
  const errors: FieldErrors<"email" | "first_name"> = {};
  const email = i.email.trim();
  if (email && !EMAIL.test(email)) errors.email = "Enter a valid email address";
  if (!email && !(i.first_name.trim() + i.last_name.trim())) errors.first_name = "Enter a name or an email";
  if (Object.keys(errors).length) return { ok: false, errors };
  return { ok: true, value: { first_name: i.first_name.trim(), last_name: i.last_name.trim(), ...(email ? { email } : {}) } };
}

/** amount is entered in major units ("1,250.50"); the API stores cents. */
export function parseAmountToCents(raw: string): number | null {
  const s = raw.replace(/[,\s]/g, "");
  if (s === "") return 0;
  if (!/^\d+(\.\d{1,2})?$/.test(s)) return null;
  return Math.round(Number(s) * 100);
}

export function validateOpportunity(i: { name: string; account_id: string; stage_id: string; amount: string }): Result<
  { name: string; account_id: string; stage_id: string; amount_cents: number },
  "name" | "account_id" | "stage_id" | "amount"
> {
  const errors: FieldErrors<"name" | "account_id" | "stage_id" | "amount"> = {};
  const name = i.name.trim();
  if (!name) errors.name = "Name is required";
  else if (name.length > 200) errors.name = "Name must be at most 200 characters";
  if (!i.account_id) errors.account_id = "Choose an account";
  if (!i.stage_id) errors.stage_id = "Choose a stage";
  const cents = parseAmountToCents(i.amount);
  if (cents === null) errors.amount = "Enter an amount like 12500 or 12,500.50";
  if (Object.keys(errors).length) return { ok: false, errors };
  return { ok: true, value: { name, account_id: i.account_id, stage_id: i.stage_id, amount_cents: cents as number } };
}

export function validateActivity(i: { type: string; subject: string }): Result<{ type: "note" | "call"; subject: string }, "type" | "subject"> {
  const errors: FieldErrors<"type" | "subject"> = {};
  if (i.type !== "note" && i.type !== "call") errors.type = "Choose note or call";
  if (!i.subject.trim()) errors.subject = "A subject is required";
  if (Object.keys(errors).length) return { ok: false, errors };
  return { ok: true, value: { type: i.type as "note" | "call", subject: i.subject.trim() } };
}
