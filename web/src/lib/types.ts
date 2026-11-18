export type Whoami = { tenant_id: string; user_id: string; role: string };
export type Page<T> = { items: T[]; next_cursor: string | null };

export type Account = {
  id: string; name: string; domain: string | null; industry: string | null; employee_count: number | null;
  website: string | null; owner_id: string | null; last_contact_at: string | null; created_at: string;
};
export type Contact = {
  id: string; first_name: string; last_name: string; email: string | null; phone: string | null; title: string | null;
  account_id: string | null; owner_id: string | null; do_not_contact: boolean; consent_status: string;
  lawful_basis: string | null; consent_source: string | null; consent_captured_at: string | null; created_at: string;
};
export type Stage = { id: string; pipeline_id: string; name: string; ordinal: number; phase: string };
export type Pipeline = { id: string; name: string; is_default: boolean; stages: Stage[] };
export type Opportunity = {
  id: string; name: string; account_id: string; pipeline_id: string; stage_id: string; amount_cents: number;
  currency: string; close_date: string | null; owner_id: string | null; status: "open" | "won" | "lost"; created_at: string;
};
export type Activity = {
  id: string; type: string; subject: string; body: string; occurred_at: string; contact_id: string | null;
  opportunity_id: string | null; account_id: string; created_by: string; created_at: string;
};
export type Lead = {
  id: string; email: string; first_name: string; last_name: string; company: string | null; domain: string | null;
  source: string; consent_status: string; lawful_basis: string | null; created_at: string;
  possible_duplicate_of: string | null; converted_at: string | null; converted_opportunity_id: string | null;
  score: { score: number; model_version: string; preliminary: boolean; reasons: { feature: string; direction: string }[] } | null;
  can_contact?: { allowed: boolean; reason: string };
};
export type SearchResult = { type: "account" | "contact" | "opportunity"; id: string; title: string; subtitle: string };
export type User = { id: string; display_name: string | null; external_subject: string; role: string };
export type Dashboard = {
  pipeline_name: string; open_count: number; open_value_cents: number;
  stages: { stage_id: string; name: string; phase: string; count: number; value_cents: number }[];
  my_open_deals: Opportunity[]; recent_activities: Activity[];
};
