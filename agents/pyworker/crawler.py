"""Native public crawler. Fetches robots.txt first and respects it, rate
limits per host, never follows a redirect off-site, caps body size, and
refuses non-public addresses. Paid third-party enrichment APIs are NOT used;
RDAP (public registry data) is used only for domain age."""
import hashlib
import json
import socket
import time
import urllib.robotparser
from dataclasses import dataclass, field
from datetime import datetime, timezone
from html.parser import HTMLParser
from typing import Callable, Optional
from urllib.parse import urlparse

import httpx

from .netguard import BlockedAddress, InvalidDomain, resolve_public, valid_domain

MAX_BODY = 512_000
MAX_ROBOTS = 100_000


@dataclass
class CrawlResult:
    status: str  # ok | blocked_robots | blocked_network | error
    http_status: Optional[int] = None
    html_sha256: Optional[str] = None
    parsed: dict = field(default_factory=dict)


class _Page(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.title = ""
        self.meta = {}
        self.jsonld = []
        self._in_title = False
        self._in_ld = False
        self._buf = []

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == "title":
            self._in_title = True
        elif tag == "meta":
            key = (a.get("name") or a.get("property") or "").lower()
            if key and a.get("content"):
                self.meta.setdefault(key, a["content"].strip()[:500])
        elif tag == "script" and (a.get("type") or "").lower() == "application/ld+json":
            self._in_ld = True
            self._buf = []

    def handle_endtag(self, tag):
        if tag == "title":
            self._in_title = False
        elif tag == "script" and self._in_ld:
            self._in_ld = False
            try:
                self.jsonld.append(json.loads("".join(self._buf)))
            except ValueError:
                pass

    def handle_data(self, data):
        if self._in_title and len(self.title) < 300:
            self.title += data.strip()
        elif self._in_ld:
            self._buf.append(data)


def _employees(node) -> Optional[int]:
    if isinstance(node, list):
        for n in node:
            v = _employees(n)
            if v:
                return v
        return None
    if not isinstance(node, dict):
        return None
    ne = node.get("numberOfEmployees")
    if isinstance(ne, (int, float)) and ne > 0:
        return int(ne)
    if isinstance(ne, str) and ne.isdigit():
        return int(ne)
    if isinstance(ne, dict):
        for k in ("value", "maxValue", "minValue"):
            v = ne.get(k)
            if isinstance(v, (int, float)) and v > 0:
                return int(v)
    for v in node.values():
        if isinstance(v, (dict, list)):
            r = _employees(v)
            if r:
                return r
    return None


def parse_html(body: bytes) -> dict:
    p = _Page()
    try:
        p.feed(body.decode("utf-8", errors="replace"))
    except Exception:  # malformed markup: keep what was parsed
        pass
    out = {
        "title": p.title[:300] or None,
        "description": p.meta.get("description") or p.meta.get("og:description"),
        "site_name": p.meta.get("og:site_name"),
        "employees": _employees(p.jsonld),
    }
    return {k: v for k, v in out.items() if v is not None}


def _site(host: str) -> str:
    return host[4:] if host.startswith("www.") else host


class Crawler:
    def __init__(
        self,
        *,
        user_agent: str,
        allow_private: bool = False,  # tests only; never set from env
        scheme: str = "https",
        port: Optional[int] = None,
        timeout: float = 5.0,
        resolver: Callable = socket.getaddrinfo,
        rate_limit: Optional[Callable[[str], bool]] = None,
    ):
        self.ua = user_agent
        self.allow_private = allow_private
        self.scheme = scheme
        self.port = port
        self.timeout = timeout
        self.resolver = resolver
        self.rate_limit = rate_limit

    # -- network ----------------------------------------------------------
    def _ips(self, host: str):
        if self.allow_private:
            return [i[4][0] for i in self.resolver(host, None, type=socket.SOCK_STREAM)][:1]
        return resolve_public(host, self.resolver)

    def _get(self, host: str, path: str, limit: int):
        """GET host+path by connecting to the VALIDATED ip (no second DNS
        lookup, so no rebinding window), keeping Host/SNI for the real name."""
        ip = self._ips(host)[0]
        netloc = f"[{ip}]" if ":" in ip else ip
        if self.port:
            netloc += f":{self.port}"
        url = f"{self.scheme}://{netloc}{path}"
        if self.rate_limit is not None:
            deadline = time.monotonic() + 3
            while not self.rate_limit(host):
                if time.monotonic() > deadline:
                    raise TimeoutError("per-host rate limit")
                time.sleep(0.25)
        with httpx.Client(timeout=self.timeout, follow_redirects=False,
                          headers={"User-Agent": self.ua, "Host": host}) as c:
            with c.stream("GET", url, extensions={"sni_hostname": host}) as r:
                body = b""
                for chunk in r.iter_bytes():
                    body += chunk
                    if len(body) >= limit:
                        body = body[:limit]
                        break
                return r.status_code, dict(r.headers), body

    # -- public API -------------------------------------------------------
    def fetch_company(self, domain: str) -> CrawlResult:
        try:
            host = valid_domain(domain)
            code, _, robots = self._get(host, "/robots.txt", MAX_ROBOTS)
        except (InvalidDomain, BlockedAddress):
            return CrawlResult("blocked_network")
        except Exception:
            return CrawlResult("error")

        rp = urllib.robotparser.RobotFileParser()
        if code in (401, 403) or code >= 500:
            return CrawlResult("blocked_robots", http_status=code)  # cannot confirm permission
        if code == 200:
            rp.parse(robots.decode("utf-8", errors="replace").splitlines())
            if not rp.can_fetch(self.ua, "/"):
                return CrawlResult("blocked_robots", http_status=code)
        # 404 etc.: no robots file = no restrictions

        try:
            path = "/"
            for _ in range(3):
                code, headers, body = self._get(host, path, MAX_BODY)
                if code in (301, 302, 303, 307, 308) and headers.get("location"):
                    loc = urlparse(headers["location"])
                    if loc.hostname and _site(loc.hostname.lower()) != _site(host):
                        return CrawlResult("ok", http_status=code)  # never follow off-site
                    path = loc.path or "/"
                    if not rp.can_fetch(self.ua, path):
                        return CrawlResult("blocked_robots", http_status=code)
                    continue
                break
            if code != 200:
                return CrawlResult("ok", http_status=code)
            return CrawlResult("ok", http_status=code,
                               html_sha256=hashlib.sha256(body).hexdigest(), parsed=parse_html(body))
        except BlockedAddress:
            return CrawlResult("blocked_network")
        except Exception:
            return CrawlResult("error")


def rdap_domain_age_days(domain: str, client: Optional[httpx.Client] = None) -> Optional[int]:
    """Domain age from public RDAP data (documented as allowed)."""
    try:
        d = valid_domain(domain)
        own = client is None
        client = client or httpx.Client(timeout=4.0, follow_redirects=True,
                                        headers={"Accept": "application/rdap+json"})
        try:
            r = client.get(f"https://rdap.org/domain/{d}")
        finally:
            if own:
                client.close()
        if r.status_code != 200:
            return None
        for ev in r.json().get("events", []):
            if ev.get("eventAction") == "registration":
                dt = datetime.fromisoformat(ev["eventDate"].replace("Z", "+00:00"))
                return max(0, (datetime.now(timezone.utc) - dt).days)
    except Exception:
        return None
    return None
