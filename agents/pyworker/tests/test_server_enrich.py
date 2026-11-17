import json
import threading
import urllib.error
import urllib.request

import pytest

from pyworker.crawler import CrawlResult
from pyworker.enrich import Enricher, cache_key
from pyworker.model import Model
from pyworker.server import make_server

TOKEN = "s3cret-token"


@pytest.fixture(scope="module")
def srv():
    s = make_server("127.0.0.1", 0, TOKEN, Model())
    threading.Thread(target=s.serve_forever, daemon=True).start()
    yield f"http://127.0.0.1:{s.server_address[1]}"
    s.shutdown()


def post(url, body, token=TOKEN):
    req = urllib.request.Request(url + "/score", data=json.dumps(body).encode(), method="POST",
                                 headers={"Authorization": f"Bearer {token}"} if token else {})
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, json.loads(r.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read())


def test_score_requires_the_service_token(srv):
    assert post(srv, {"lead": {"email": "a@acme.io"}}, token=None)[0] == 401
    assert post(srv, {"lead": {"email": "a@acme.io"}}, token="wrong")[0] == 401


def test_score_endpoint_returns_a_versioned_score(srv):
    code, out = post(srv, {"lead": {"email": "a@acme.io", "source": "referral"}, "matches_open_opportunity_account": True})
    assert code == 200 and out["model_version"].startswith("synthetic-") and 0 <= out["score"] <= 100
    assert post(srv, {"lead": {"email": "a@acme.io", "source": "referral"}, "matches_open_opportunity_account": True})[1] == out


def test_score_endpoint_is_reproducible_from_returned_features(srv):
    _, first = post(srv, {"lead": {"email": "z@gmail.com", "source": "paid"}})
    _, again = post(srv, {"features": first["features"]})
    assert (again["score"], again["reasons"]) == (first["score"], first["reasons"])


def test_bad_requests_are_400_and_unknown_paths_404(srv):
    req = urllib.request.Request(srv + "/score", data=b"not json", method="POST", headers={"Authorization": f"Bearer {TOKEN}"})
    with pytest.raises(urllib.error.HTTPError) as e:
        urllib.request.urlopen(req)
    assert e.value.code == 400
    with pytest.raises(urllib.error.HTTPError) as e:
        urllib.request.urlopen(srv + "/nope")
    assert e.value.code == 404


class FakeRedis:
    def __init__(self):
        self.d, self.ttl = {}, {}

    def get(self, k):
        return self.d.get(k)

    def set(self, k, v, ex=None):
        self.d[k], self.ttl[k] = v, ex


class FakeCrawler:
    def __init__(self, res):
        self.res, self.calls = res, 0

    def fetch_company(self, domain):
        self.calls += 1
        return self.res


def test_cache_miss_crawls_stores_for_seven_days_and_adds_domain_age():
    r, c = FakeRedis(), FakeCrawler(CrawlResult("ok", 200, "h" * 64, {"employees": 50}))
    e = Enricher(r, c, lambda d: 1234, 7 * 86400)
    out = e.enrich("Acme.io")
    assert not out.from_cache and out.parsed == {"employees": 50, "domain_age_days": 1234}
    assert r.ttl[cache_key("acme.io")] == 7 * 86400 and cache_key("x.io") == "global:enrich:x.io"


def test_cache_hit_does_not_crawl():
    r, c = FakeRedis(), FakeCrawler(CrawlResult("ok", 200, "h" * 64, {"employees": 50}))
    e = Enricher(r, c, lambda d: None, 100)
    e.enrich("acme.io")
    again = e.enrich("acme.io")
    assert c.calls == 1 and again.from_cache and again.parsed == {"employees": 50}


def test_errors_are_cached_only_briefly_and_blocked_robots_is_cached_long():
    r = FakeRedis()
    Enricher(r, FakeCrawler(CrawlResult("error")), lambda d: None, 7 * 86400).enrich("flaky.io")
    assert r.ttl[cache_key("flaky.io")] == 300
    Enricher(r, FakeCrawler(CrawlResult("blocked_robots", 200)), lambda d: None, 7 * 86400).enrich("closed.io")
    assert r.ttl[cache_key("closed.io")] == 7 * 86400


def test_a_broken_cache_falls_back_to_a_live_fetch():
    class Boom:
        def get(self, k):
            raise RuntimeError("redis down")

        def set(self, *a, **k):
            raise RuntimeError("redis down")

    c = FakeCrawler(CrawlResult("ok", 200, "h" * 64, {"title": "T"}))
    assert Enricher(Boom(), c, lambda d: None, 100).enrich("acme.io").parsed == {"title": "T"}
