"""SSRF guard. The crawler fetches attacker-influenced domains (they come
from a public form), so it must never reach loopback, private, link-local
(including the EC2 metadata address 169.254.169.254), CGNAT or reserved
space, directly or through DNS tricks or redirects."""
import ipaddress
import re
import socket
from typing import Callable, List

_HOST_RE = re.compile(
    r"^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$"
)


class BlockedAddress(Exception):
    pass


class InvalidDomain(Exception):
    pass


def valid_domain(domain: str) -> str:
    d = (domain or "").strip().lower().rstrip(".")
    if not _HOST_RE.match(d):  # also rejects IP literals, ports, paths, userinfo
        raise InvalidDomain(f"not a plain hostname: {domain!r}")
    return d


def is_public_ip(ip: str) -> bool:
    addr = ipaddress.ip_address(ip)
    if isinstance(addr, ipaddress.IPv6Address) and addr.ipv4_mapped:
        addr = addr.ipv4_mapped
    # is_global is False for private, loopback, link-local, CGNAT, reserved,
    # documentation and unspecified ranges.
    return addr.is_global and not addr.is_multicast


def resolve_public(host: str, resolver: Callable = socket.getaddrinfo) -> List[str]:
    """Resolve host and return its addresses; raise BlockedAddress if ANY
    resolved address is not public (a mixed answer is treated as hostile)."""
    infos = resolver(host, None, type=socket.SOCK_STREAM)
    ips = []
    for info in infos:
        ip = info[4][0]
        if not is_public_ip(ip):
            raise BlockedAddress(f"{host} resolves to a non-public address")
        if ip not in ips:
            ips.append(ip)
    if not ips:
        raise BlockedAddress(f"{host} did not resolve")
    return ips
