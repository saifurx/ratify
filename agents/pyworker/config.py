import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Config:
    database_url: str
    kafka_brokers: str
    redis_url: str
    service_token: str
    http_port: int = 8090
    crawl_user_agent: str = "RatifyBot/0.1 (+https://ratify.invalid/bot)"
    cache_ttl_seconds: int = 7 * 24 * 3600
    consumer_group: str = "pyworker-lead-scoring"

    @staticmethod
    def from_env() -> "Config":
        def need(k: str) -> str:
            v = os.environ.get(k, "")
            if not v:
                raise SystemExit(f"{k} is required")
            return v

        return Config(
            database_url=need("DATABASE_URL"),
            kafka_brokers=need("KAFKA_BROKERS"),
            redis_url=need("REDIS_URL"),
            service_token=need("PYWORKER_SERVICE_TOKEN"),
            http_port=int(os.environ.get("HTTP_PORT", "8090")),
        )
