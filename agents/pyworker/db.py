"""Database access as ml_worker: tenant-scoped, transaction-scoped binding.
This role is NOT cross-tenant: every unit of work runs in its own
`set_tenant()` transaction. Never use a session-level setting here."""
import re
from contextlib import contextmanager

from psycopg_pool import ConnectionPool

_UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")


def make_pool(url: str) -> ConnectionPool:
    # prepare_threshold=None: no server-side prepared statements (PgBouncer
    # transaction pooling).
    return ConnectionPool(url, min_size=1, max_size=4, kwargs={"prepare_threshold": None}, open=True)


@contextmanager
def tenant_tx(pool: ConnectionPool, tenant_id: str):
    tid = (tenant_id or "").lower()
    if not _UUID.match(tid):
        raise ValueError("invalid tenant id")  # fail closed: no tenant, no work
    with pool.connection() as conn:
        with conn.transaction():
            conn.execute("SELECT set_tenant(%s::uuid)", [tid])
            yield conn


def assert_not_privileged(pool: ConnectionPool, expected: str = "ml_worker") -> None:
    """Boot check, same rules as db.AssertNotPrivileged in Go."""
    with pool.connection() as conn:
        user, sup, bypass, owns = conn.execute(
            """SELECT r.rolname, r.rolsuper, r.rolbypassrls,
                      EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
                               WHERE c.relowner = r.oid AND c.relkind IN ('r','p','v','m')
                                 AND n.nspname NOT IN ('pg_catalog','information_schema','pg_toast'))
                 FROM pg_roles r WHERE r.rolname = current_user""").fetchone()
    if sup or bypass or owns or user == "app_migrator" or user != expected:
        raise SystemExit(f"refusing to run as {user!r} (super={sup} bypassrls={bypass} owner={owns}); expected {expected}")
