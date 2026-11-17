"""Kafka consumer: ratify.lead.created / ratify.lead.updated -> enrich ->
score -> write lead_scores + outbox lead.scored, exactly-once in effect.

Delivery is at-least-once, so the handler dedupes on event_id via
processed_events, inside the same transaction as its writes. The topic is
read with isolation.level=read_committed.
"""
import json
import logging
import time
import uuid
from datetime import datetime, timezone

from confluent_kafka import Consumer, KafkaError
from confluent_kafka.admin import AdminClient, NewTopic

from . import VERSION
from .db import tenant_tx
from .features import build_features

log = logging.getLogger("pyworker")
CONSUMER_NAME = "pyworker.lead_scoring"
PRODUCED_BY = f"ai:pyworker-lead-scoring@{VERSION}"
TOPICS = ["ratify.lead.created", "ratify.lead.updated"]


def _headers(msg) -> dict:
    return {k: (v.decode() if isinstance(v, bytes) else v) for k, v in (msg.headers() or [])}


def _append_outbox(conn, tenant_id: str, event_type: str, key: str, payload: dict) -> None:
    eid = str(uuid.uuid4())
    headers = {"event_id": eid, "tenant_id": tenant_id, "event_type": event_type,
               "occurred_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")}
    conn.execute(
        "INSERT INTO outbox (id, tenant_id, topic, key, payload, headers) "
        "VALUES (%s::uuid, current_tenant(), %s, %s, %s::jsonb, %s::jsonb)",
        [eid, event_type, key, json.dumps(payload), json.dumps(headers)])


class LeadScorer:
    def __init__(self, pool, enricher, model):
        self.pool, self.enricher, self.model = pool, enricher, model

    def process(self, event_id: str, tenant_id: str, lead_id: str) -> str:
        """Returns 'scored', 'duplicate' or 'skipped'."""
        with tenant_tx(self.pool, tenant_id) as conn:
            row = conn.execute(
                "SELECT email::text, domain, source, company FROM leads WHERE id = %s::uuid AND deleted_at IS NULL",
                [lead_id]).fetchone()
        if row is None:
            return "skipped"
        lead = {"email": row[0], "domain": row[1], "source": row[2], "company": row[3]}

        # Network I/O happens OUTSIDE any database transaction.
        enrichment = self.enricher.enrich(lead["domain"]) if lead["domain"] else None

        with tenant_tx(self.pool, tenant_id) as conn:
            tag = conn.execute(
                "INSERT INTO processed_events (tenant_id, consumer, event_id) "
                "VALUES (current_tenant(), %s, %s::uuid) ON CONFLICT DO NOTHING",
                [CONSUMER_NAME, event_id])
            if tag.rowcount == 0:
                return "duplicate"
            if enrichment is not None:
                conn.execute(
                    "INSERT INTO enrichment_raw (tenant_id, lead_id, domain, status, http_status, html_sha256, parsed) "
                    "VALUES (current_tenant(), %s::uuid, %s, %s, %s, %s, %s::jsonb)",
                    [lead_id, lead["domain"], "cached" if enrichment.from_cache else enrichment.status,
                     enrichment.http_status, enrichment.html_sha256, json.dumps(enrichment.parsed)])
            matches = False
            if lead["domain"]:
                matches = conn.execute(
                    "SELECT EXISTS (SELECT 1 FROM accounts a JOIN opportunities o "
                    "ON o.tenant_id = a.tenant_id AND o.account_id = a.id "
                    "WHERE lower(a.domain) = lower(%s) AND o.status = 'open')", [lead["domain"]]).fetchone()[0]
            feats = build_features(lead, {"parsed": enrichment.parsed} if enrichment else None, matches)
            score, reasons = self.model.score(feats)
            conn.execute(
                "INSERT INTO lead_scores (tenant_id, lead_id, score, reasons, features, model_version, produced_by) "
                "VALUES (current_tenant(), %s::uuid, %s, %s::jsonb, %s::jsonb, %s, %s)",
                [lead_id, score, json.dumps(reasons), json.dumps(feats), self.model.version, PRODUCED_BY])
            conn.execute(
                "INSERT INTO audit_log (tenant_id, actor, action, subject_type, subject_id, after) "
                "VALUES (current_tenant(), %s, 'lead.scored', 'lead', %s::uuid, %s::jsonb)",
                [PRODUCED_BY, lead_id, json.dumps({"score": score, "model_version": self.model.version})])
            _append_outbox(conn, tenant_id, "ratify.lead.scored", lead_id,
                           {"lead_id": lead_id, "score": score, "model_version": self.model.version})
        return "scored"


def ensure_topics(brokers: str, topics) -> None:
    """Create the input topics if the relay has not published to them yet
    (single-broker, RF=1). Existing topics are fine."""
    try:
        admin = AdminClient({"bootstrap.servers": brokers})
        for f in admin.create_topics([NewTopic(t, num_partitions=1, replication_factor=1) for t in topics]).values():
            try:
                f.result(10)
            except Exception:  # noqa: BLE001  (already exists / broker busy: not fatal)
                pass
    except Exception as e:  # noqa: BLE001
        log.warning("topic pre-create skipped: %s", type(e).__name__)


def run(cfg, scorer: LeadScorer, stop) -> None:
    ensure_topics(cfg.kafka_brokers, TOPICS)
    consumer = Consumer({
        "bootstrap.servers": cfg.kafka_brokers, "group.id": cfg.consumer_group,
        "enable.auto.commit": False, "auto.offset.reset": "earliest",
        "isolation.level": "read_committed", "allow.auto.create.topics": True,
    })
    consumer.subscribe(TOPICS)
    log.info("consumer started topics=%s", TOPICS)
    try:
        while not stop.is_set():
            msg = consumer.poll(1.0)
            if msg is None:
                continue
            if msg.error():
                if msg.error().code() != KafkaError.UNKNOWN_TOPIC_OR_PART:  # topics appear with the first event
                    log.error("kafka error: %s", msg.error())
                continue
            h = _headers(msg)
            ok = False
            for attempt in range(1, 4):
                try:
                    payload = json.loads(msg.value())
                    res = scorer.process(h["event_id"], h["tenant_id"], payload["lead_id"])
                    log.info("event processed result=%s", res)  # ids only, never PII
                    ok = True
                    break
                except Exception as e:  # noqa: BLE001
                    log.error("event failed attempt=%d err=%s", attempt, type(e).__name__)
                    time.sleep(min(2 ** attempt, 5))
            if not ok:
                log.error("pyworker_dead_letter topic=%s offset=%s", msg.topic(), msg.offset())
            consumer.commit(message=msg, asynchronous=False)
    finally:
        consumer.close()
