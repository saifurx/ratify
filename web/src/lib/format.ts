export function formatCents(cents: number, currency = "USD"): string {
  return new Intl.NumberFormat("en-US", { style: "currency", currency, maximumFractionDigits: 0 }).format(cents / 100);
}

export function formatDate(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : d.toISOString().slice(0, 10);
}

export function formatDateTime(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : d.toISOString().slice(0, 16).replace("T", " ") + " UTC";
}

/** Human explanation for each consent.CanContact reason code. */
export function reasonText(reason?: string): string {
  switch (reason) {
    case "ok":
      return "Consent on record";
    case "suppressed":
      return "This address is on the suppression list.";
    case "do_not_contact":
      return "A contact with this email is marked do not contact.";
    case "consent_revoked":
      return "Consent was revoked.";
    case "no_consent":
      return "No consent or lawful basis is recorded for this email.";
    case "insufficient_basis":
      return "The recorded basis does not permit contact.";
    default:
      return "Consent status unknown.";
  }
}
