import socket
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from pyworker.crawler import Crawler, parse_html

UA = "RatifyBot/0.1"


def local_resolver(host, port, type=0):
    return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("127.0.0.1", 0))]


class Site:
    """A throwaway site that records every request path it receives."""

    def __init__(self, routes):
        self.requests = []
        site = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def do_GET(self):
                site.requests.append((self.path, self.headers.get("Host")))
                code, headers, body = routes.get(self.path, (404, {}, b""))
                self.send_response(code)
                for k, v in headers.items():
                    self.send_header(k, v)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        self.port = self.srv.server_address[1]
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

    def close(self):
        self.srv.shutdown()

    def paths(self):
        return [p for p, _ in self.requests]


@pytest.fixture
def make():
    sites = []

    def _make(routes, **kw):
        s = Site(routes)
        sites.append(s)
        c = Crawler(user_agent=UA, allow_private=True, scheme="http", port=s.port, resolver=local_resolver, **kw)
        return s, c

    yield _make
    for s in sites:
        s.close()


PAGE = (b"<html><head><title>Acme Corp</title><meta name='description' content='We make anvils'>"
        b"<meta property='og:site_name' content='Acme'>"
        b"<script type='application/ld+json'>{\"@type\":\"Organization\",\"numberOfEmployees\":{\"value\":250}}</script>"
        b"</head><body>hi</body></html>")


def test_robots_disallow_means_the_page_is_never_fetched(make):
    site, crawler = make({"/robots.txt": (200, {}, b"User-agent: *\nDisallow: /\n"), "/": (200, {}, PAGE)})
    res = crawler.fetch_company("acme.test")
    assert res.status == "blocked_robots"
    assert "/" not in site.paths(), f"the disallowed page was fetched: {site.paths()}"
    assert site.paths() == ["/robots.txt"]


def test_robots_is_fetched_first_and_an_allowed_page_is_parsed(make):
    site, crawler = make({"/robots.txt": (200, {}, b"User-agent: *\nDisallow: /private\n"), "/": (200, {}, PAGE)})
    res = crawler.fetch_company("acme.test")
    assert site.paths() == ["/robots.txt", "/"]
    assert res.status == "ok" and res.http_status == 200 and len(res.html_sha256) == 64
    assert res.parsed == {"title": "Acme Corp", "description": "We make anvils", "site_name": "Acme", "employees": 250}


def test_a_rule_for_our_agent_is_honoured(make):
    site, crawler = make({"/robots.txt": (200, {}, b"User-agent: RatifyBot\nDisallow: /\n\nUser-agent: *\nAllow: /\n"),
                          "/": (200, {}, PAGE)})
    assert crawler.fetch_company("acme.test").status == "blocked_robots"
    assert site.paths() == ["/robots.txt"]


def test_no_robots_file_means_no_restriction(make):
    site, crawler = make({"/": (200, {}, PAGE)})
    assert crawler.fetch_company("acme.test").status == "ok"
    assert site.paths() == ["/robots.txt", "/"]


@pytest.mark.parametrize("code", [403, 500, 503])
def test_unconfirmable_robots_is_treated_as_blocked(make, code):
    site, crawler = make({"/robots.txt": (code, {}, b""), "/": (200, {}, PAGE)})
    assert crawler.fetch_company("acme.test").status == "blocked_robots"
    assert "/" not in site.paths()


def test_the_real_hostname_is_sent_as_host_header(make):
    site, crawler = make({"/": (200, {}, PAGE)})
    crawler.fetch_company("acme.test")
    assert {h for _, h in site.requests} == {f"acme.test"}


def test_off_site_redirects_are_not_followed(make):
    site, crawler = make({"/": (302, {"Location": "http://169.254.169.254/latest/meta-data/"}, b"")})
    res = crawler.fetch_company("acme.test")
    assert res.http_status == 302 and res.parsed == {}
    assert site.paths() == ["/robots.txt", "/"]


def test_same_site_redirect_is_followed_and_robots_rechecked(make):
    site, crawler = make({"/robots.txt": (200, {}, b"User-agent: *\nDisallow: /secret\n"),
                          "/": (301, {"Location": "http://www.acme.test/secret"}, b"")})
    assert crawler.fetch_company("acme.test").status == "blocked_robots"
    assert "/secret" not in site.paths()


def test_oversized_bodies_are_truncated(make):
    big = b"<html><title>Big</title>" + b"x" * 2_000_000
    _, crawler = make({"/": (200, {}, big)})
    assert crawler.fetch_company("acme.test").status == "ok"


def test_per_host_rate_limit_is_consulted_for_every_request(make):
    calls = []
    _, crawler = make({"/": (200, {}, PAGE)}, rate_limit=lambda h: calls.append(h) or True)
    crawler.fetch_company("acme.test")
    assert calls == ["acme.test", "acme.test"]  # robots.txt + page


def test_non_public_targets_are_refused_before_any_connection():
    c = Crawler(user_agent=UA, resolver=lambda h, p, type=0: [(socket.AF_INET, 1, 6, "", ("169.254.169.254", 0))])
    assert c.fetch_company("metadata.example.com").status == "blocked_network"
    assert Crawler(user_agent=UA).fetch_company("127.0.0.1").status == "blocked_network"  # IP literal
    assert Crawler(user_agent=UA).fetch_company("http://evil.com/").status == "blocked_network"


def test_parse_html_tolerates_garbage():
    assert parse_html(b"\x00\xff<<<not html>>> <script type='application/ld+json'>{bad") == {}
