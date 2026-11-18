import { Board } from "@/components/board";
import { apiGet } from "@/lib/api";
import { canWrite } from "@/lib/roles";
import type { Account, Opportunity, Page, Pipeline, Whoami } from "@/lib/types";

export default async function PipelinePage({ searchParams }: { searchParams: Promise<{ pipeline?: string }> }) {
  const { pipeline: pipelineParam } = await searchParams;
  const me = await apiGet<Whoami>("/api/whoami");
  const pipelines = await apiGet<Page<Pipeline>>("/api/v1/pipelines");
  const current = pipelines.items.find((p) => p.id === pipelineParam) ?? pipelines.items.find((p) => p.is_default) ?? pipelines.items[0];
  if (!current) {
    return <p className="text-sm text-muted-foreground">No pipeline is configured yet. An admin can create one.</p>;
  }
  const [opps, accts] = await Promise.all([
    apiGet<Page<Opportunity>>(`/api/v1/opportunities?pipeline_id=${current.id}&limit=200`),
    apiGet<Page<Account>>("/api/v1/accounts?limit=200"),
  ]);
  const names = Object.fromEntries(accts.items.map((a) => [a.id, a.name]));
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">Pipeline: {current.name}</h1>
          <p className="text-sm text-muted-foreground">{canWrite(me.role) ? "Drag a card to another stage, or use the stage menu." : "Read-only view."}</p>
        </div>
        {pipelines.items.length > 1 ? (
          <nav className="flex gap-2 text-sm" aria-label="Pipelines">
            {pipelines.items.map((p) => (
              <a key={p.id} href={`/opportunities?pipeline=${p.id}`} className={p.id === current.id ? "font-medium text-primary" : "text-muted-foreground hover:text-foreground"}>
                {p.name}
              </a>
            ))}
          </nav>
        ) : null}
      </div>
      <Board key={current.id} stages={current.stages} initial={opps.items} accountNames={names} canWrite={canWrite(me.role)} />
    </div>
  );
}
