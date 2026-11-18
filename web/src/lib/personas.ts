// Dev-mode login personas. They mirror `make seed-demo` (cmd/seed-demo).
// Dev login exists ONLY when OIDC_ISSUER is empty and APP_ENV != production,
// exactly like the API's dev mode. Tenant ids here are used server-side only.
export type Persona = { id: string; label: string; tenantLabel: string; tenantId: string; subject: string; role: string };

const TENANTS = [
  { prefix: "a", label: "Demo Company A", id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" },
  { prefix: "b", label: "Demo Company B", id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" },
];

const PEOPLE = [
  { key: "admin", role: "admin", label: "Alex Admin" },
  { key: "manager1", role: "manager", label: "Morgan East (manager)" },
  { key: "manager2", role: "manager", label: "Marion West (manager)" },
  { key: "rep1", role: "rep", label: "Riley Rep (East)" },
  { key: "rep2", role: "rep", label: "Robin Rep (East)" },
  { key: "rep3", role: "rep", label: "Rowan Rep (West)" },
  { key: "rep4", role: "rep", label: "Remy Rep (West)" },
  { key: "finance", role: "finance", label: "Frankie Finance (read-only)" },
  { key: "legal", role: "legal", label: "Logan Legal (read-only)" },
];

export const PERSONAS: Persona[] = TENANTS.flatMap((t) =>
  PEOPLE.map((p) => ({
    id: `${t.prefix}-${p.key}`,
    label: p.label,
    tenantLabel: t.label,
    tenantId: t.id,
    subject: `${t.prefix}-${p.key}`,
    role: p.role,
  })),
);

export function findPersona(id: string): Persona | undefined {
  return PERSONAS.find((p) => p.id === id);
}
