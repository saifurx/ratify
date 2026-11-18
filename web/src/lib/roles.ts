// The UI MIRRORS the API's authz.Scope; it never replaces it. Hiding a
// button is a courtesy: the server enforces every rule.
export type Role = "admin" | "manager" | "rep" | "finance" | "legal" | "agent" | "system";

export const canWrite = (role?: string) => role === "admin" || role === "manager" || role === "rep";
export const isAdmin = (role?: string) => role === "admin";
export const canListUsers = (role?: string) => role !== "rep" && role !== undefined;

export function scopeLabel(role?: string): string {
  switch (role) {
    case "rep":
      return "Your own records";
    case "manager":
      return "Your team's records";
    case "admin":
      return "Everything in your organisation";
    case "finance":
    case "legal":
      return "Everything in your organisation (read-only)";
    default:
      return "";
  }
}
