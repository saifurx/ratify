#!/usr/bin/env python3
"""Sum compose mem_limit; fail above the cap or on missing limits/unpinned images."""
import json, subprocess, sys

CAP_GIB = 13.5
raw = subprocess.check_output(
    ["docker", "compose", "--env-file", "deploy/.env", "-f", "deploy/docker-compose.yml",
     "--profile", "*", "config", "--format", "json"])
services = json.loads(raw)["services"]
total, bad = 0, []
print(f"{'service':14} {'mem':>8} {'cpus':>5}  image")
for name, s in sorted(services.items()):
    mem, cpus, img = s.get("mem_limit"), s.get("cpus"), s.get("image", "")
    if not mem: bad.append(f"{name}: missing mem_limit")
    if not cpus: bad.append(f"{name}: missing cpus")
    tag = img.rsplit(":", 1)[-1] if ":" in img.split("/")[-1] else ""
    if not tag or tag == "latest": bad.append(f"{name}: image not pinned ({img})")
    mem = int(mem or 0)
    total += mem
    print(f"{name:14} {mem/2**20:6.0f}Mi {cpus!s:>5}  {img}")
gib = total / 2**30
print(f"TOTAL {gib:.2f} GiB (cap {CAP_GIB})")
if gib > CAP_GIB: bad.append(f"total {gib:.2f} GiB exceeds {CAP_GIB} GiB")
if bad:
    print("FAIL:\n  " + "\n  ".join(bad)); sys.exit(1)
