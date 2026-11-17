import logging
import signal
import threading

import redis

from . import VERSION
from .config import Config
from .crawler import Crawler, rdap_domain_age_days
from .db import assert_not_privileged, make_pool
from .enrich import Enricher
from .model import Model
from .server import make_server
from .worker import LeadScorer, run


def main() -> None:
    logging.basicConfig(level=logging.INFO, format='{"level":"%(levelname)s","logger":"%(name)s","msg":"%(message)s"}')
    log = logging.getLogger("pyworker")
    cfg = Config.from_env()
    pool = make_pool(cfg.database_url)
    assert_not_privileged(pool)  # fail closed at boot
    rdb = redis.Redis.from_url(cfg.redis_url)
    rdb.ping()
    model = Model()
    log.info("model ready version=%s worker=%s", model.version, VERSION)

    def per_host(host: str) -> bool:  # 1 request / 2 s / host, across workers
        return bool(rdb.set(f"global:enrich:rl:{host}", 1, nx=True, ex=2))

    crawler = Crawler(user_agent=cfg.crawl_user_agent, rate_limit=per_host)
    enricher = Enricher(rdb, crawler, rdap_domain_age_days, cfg.cache_ttl_seconds)

    server = make_server("0.0.0.0", cfg.http_port, cfg.service_token, model)
    threading.Thread(target=server.serve_forever, daemon=True).start()

    stop = threading.Event()
    for s in (signal.SIGINT, signal.SIGTERM):
        signal.signal(s, lambda *_: stop.set())
    run(cfg, LeadScorer(pool, enricher, model), stop)
    server.shutdown()


if __name__ == "__main__":
    main()
