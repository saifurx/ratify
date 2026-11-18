import { describe, expect, it } from "vitest";
import { parseAmountToCents, validateAccount, validateActivity, validateContact, validateOpportunity } from "@/lib/validation";

describe("validateAccount", () => {
  it("requires a name", () => {
    const r = validateAccount({ name: "   " });
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.errors.name).toBe("Name is required");
  });
  it("rejects bad domains and negative or fractional employee counts", () => {
    const r = validateAccount({ name: "Acme", domain: "not a domain", employee_count: "-3" });
    expect(r.ok).toBe(false);
    if (!r.ok) {
      expect(r.errors.domain).toMatch(/domain/i);
      expect(r.errors.employee_count).toMatch(/whole number/);
    }
    expect(validateAccount({ name: "A", employee_count: "1.5" }).ok).toBe(false);
  });
  it("trims and returns only provided optional fields", () => {
    expect(validateAccount({ name: "  Acme ", domain: "acme.com", employee_count: "50" })).toEqual({
      ok: true,
      value: { name: "Acme", domain: "acme.com", employee_count: 50 },
    });
    expect(validateAccount({ name: "Acme" })).toEqual({ ok: true, value: { name: "Acme" } });
  });
  it("caps the name at 200 characters", () => {
    expect(validateAccount({ name: "x".repeat(201) }).ok).toBe(false);
  });
});

describe("validateContact", () => {
  it("needs a name or an email", () => {
    const r = validateContact({ first_name: "", last_name: "", email: "" });
    expect(r.ok).toBe(false);
  });
  it("validates the email format", () => {
    const r = validateContact({ first_name: "A", last_name: "", email: "nope" });
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.errors.email).toMatch(/valid email/);
  });
  it("accepts an email alone", () => {
    expect(validateContact({ first_name: "", last_name: "", email: "a@b.co" }).ok).toBe(true);
  });
});

describe("amounts and opportunities", () => {
  it("parses major units to cents", () => {
    expect(parseAmountToCents("12,500.50")).toBe(1250050);
    expect(parseAmountToCents("")).toBe(0);
    expect(parseAmountToCents("0.1")).toBe(10);
    expect(parseAmountToCents("12.345")).toBeNull();
    expect(parseAmountToCents("abc")).toBeNull();
    expect(parseAmountToCents("-5")).toBeNull();
  });
  it("requires name, account and stage", () => {
    const r = validateOpportunity({ name: "", account_id: "", stage_id: "", amount: "x" });
    expect(r.ok).toBe(false);
    if (!r.ok) expect(Object.keys(r.errors).sort()).toEqual(["account_id", "amount", "name", "stage_id"]);
  });
  it("produces an API-shaped payload", () => {
    expect(validateOpportunity({ name: " Deal ", account_id: "a", stage_id: "s", amount: "1,000" })).toEqual({
      ok: true,
      value: { name: "Deal", account_id: "a", stage_id: "s", amount_cents: 100000 },
    });
  });
  it("never produces a tenant_id field", () => {
    const r = validateOpportunity({ name: "x", account_id: "a", stage_id: "s", amount: "1" });
    expect(JSON.stringify(r)).not.toMatch(/tenant/i);
  });
});

describe("validateActivity", () => {
  it("only allows notes and call logs (email send arrives in Phase 2)", () => {
    expect(validateActivity({ type: "email_sent", subject: "x" }).ok).toBe(false);
    expect(validateActivity({ type: "note", subject: "" }).ok).toBe(false);
    expect(validateActivity({ type: "call", subject: " Called " })).toEqual({ ok: true, value: { type: "call", subject: "Called" } });
  });
});
