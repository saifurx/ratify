import "server-only";
import { apiGet } from "@/lib/api";
import { canListUsers } from "@/lib/roles";
import type { Page, User, Whoami } from "@/lib/types";

/** id -> display name. Reps may not list users; they only ever see their own records. */
export async function ownerNames(me: Whoami): Promise<Record<string, string>> {
  const out: Record<string, string> = { [me.user_id]: "You" };
  if (canListUsers(me.role)) {
    const users = await apiGet<Page<User>>("/api/v1/users");
    for (const u of users.items) if (u.id !== me.user_id) out[u.id] = u.display_name ?? u.external_subject;
  }
  return out;
}
