import socket

import pytest

from pyworker.netguard import BlockedAddress, InvalidDomain, is_public_ip, resolve_public, valid_domain


@pytest.mark.parametrize("ip", [
    "127.0.0.1", "10.0.0.5", "172.16.3.4", "192.168.1.1", "169.254.169.254",  # EC2 metadata
    "100.64.0.1", "0.0.0.0", "224.0.0.1", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:10.1.2.3",
])
def test_non_public_addresses_are_blocked(ip):
    assert not is_public_ip(ip)


@pytest.mark.parametrize("ip", ["8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"])
def test_public_addresses_are_allowed(ip):
    assert is_public_ip(ip)


def fake(*ips):
    def resolver(host, port, type=0):
        return [(socket.AF_INET6 if ":" in ip else socket.AF_INET, socket.SOCK_STREAM, 6, "", (ip, 0)) for ip in ips]
    return resolver


def test_hostname_resolving_to_private_space_is_blocked():
    with pytest.raises(BlockedAddress):
        resolve_public("evil.example.com", fake("10.0.0.1"))


def test_a_mixed_dns_answer_is_treated_as_hostile():
    with pytest.raises(BlockedAddress):
        resolve_public("rebind.example.com", fake("93.184.216.34", "169.254.169.254"))


def test_public_resolution_returns_addresses():
    assert resolve_public("ok.example.com", fake("93.184.216.34")) == ["93.184.216.34"]


@pytest.mark.parametrize("bad", ["127.0.0.1", "http://acme.io", "acme.io/path", "acme.io:8080", "user@acme.io",
                                 "localhost", "a b.com", "", "[::1]", "-bad.com", "x" * 300 + ".com"])
def test_only_plain_hostnames_are_valid(bad):
    with pytest.raises(InvalidDomain):
        valid_domain(bad)


def test_valid_domain_normalises():
    assert valid_domain(" Acme.IO. ") == "acme.io"
