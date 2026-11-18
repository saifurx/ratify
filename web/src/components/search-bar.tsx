"use client";
import { Search } from "lucide-react";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { bff } from "@/lib/client";
import { errorMessage } from "@/lib/errors";
import type { Page, SearchResult } from "@/lib/types";

const HREF: Record<SearchResult["type"], (id: string) => string> = {
  account: (id) => `/accounts/${id}`,
  contact: (id) => `/contacts/${id}`,
  opportunity: () => `/opportunities`,
};

export function SearchBar() {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<SearchResult[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (q.trim().length < 2) {
      setResults(null);
      return;
    }
    const t = setTimeout(async () => {
      try {
        const r = await bff<Page<SearchResult>>(`/search?q=${encodeURIComponent(q)}`);
        setResults(r.items);
        setError(null);
      } catch (e) {
        setError(errorMessage(e));
      }
    }, 200);
    return () => clearTimeout(t);
  }, [q]);

  useEffect(() => {
    const close = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) setResults(null);
    };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, []);

  return (
    <div ref={box} className="relative w-full max-w-md">
      <Search className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" aria-hidden />
      <input
        type="search"
        aria-label="Search"
        placeholder="Search accounts, contacts, opportunities"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        className="h-9 w-full rounded-md border border-border bg-white pl-9 pr-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
      />
      {error ? <p role="alert" className="absolute mt-1 text-xs text-destructive">{error}</p> : null}
      {results ? (
        <ul role="listbox" aria-label="Search results" className="absolute z-20 mt-1 max-h-96 w-full overflow-auto rounded-md border border-border bg-white shadow-lg">
          {results.length === 0 ? <li className="px-3 py-2 text-sm text-muted-foreground">No results</li> : null}
          {results.map((r) => (
            <li key={`${r.type}-${r.id}`} role="option" aria-selected={false}>
              <Link href={HREF[r.type](r.id)} className="flex items-center justify-between gap-3 px-3 py-2 text-sm hover:bg-muted" onClick={() => setResults(null)}>
                <span className="truncate">
                  <span className="font-medium">{r.title}</span>
                  <span className="ml-2 text-xs text-muted-foreground">{r.subtitle}</span>
                </span>
                <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase text-muted-foreground">{r.type}</span>
              </Link>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
