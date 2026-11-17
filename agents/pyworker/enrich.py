"""Enrichment orchestration with a 7-day Redis cache.

The cache key `global:enrich:{domain}` is intentionally cross-tenant: it
holds only public website data about a domain (documented in CLAUDE.md)."""
import json
from dataclasses import dataclass, field
from typing import Callable, Optional

from .crawler import CrawlResult


@dataclass
class Enrichment:
    status: str
    http_status: Optional[int] = None
    html_sha256: Optional[str] = None
    parsed: dict = field(default_factory=dict)
    from_cache: bool = False


def cache_key(domain: str) -> str:
    return f"global:enrich:{domain.lower()}"


class Enricher:
    def __init__(self, redis, crawler, rdap: Callable[[str], Optional[int]], ttl: int):
        self.redis, self.crawler, self.rdap, self.ttl = redis, crawler, rdap, ttl

    def enrich(self, domain: str) -> Enrichment:
        key = cache_key(domain)
        try:
            hit = self.redis.get(key)
        except Exception:
            hit = None  # cache down: fall through to a live fetch
        if hit:
            d = json.loads(hit)
            return Enrichment(d["status"], d.get("http_status"), d.get("html_sha256"), d.get("parsed", {}), True)

        res: CrawlResult = self.crawler.fetch_company(domain)
        parsed = dict(res.parsed)
        if res.status in ("ok", "blocked_robots"):
            age = self.rdap(domain)
            if age is not None:
                parsed["domain_age_days"] = age
        out = Enrichment(res.status, res.http_status, res.html_sha256, parsed, False)
        # Errors are not cached for long: a transient failure should be retried soon.
        ttl = self.ttl if res.status in ("ok", "blocked_robots", "blocked_network") else 300
        try:
            self.redis.set(key, json.dumps({"status": out.status, "http_status": out.http_status,
                                            "html_sha256": out.html_sha256, "parsed": out.parsed}), ex=ttl)
        except Exception:
            pass
        return out
