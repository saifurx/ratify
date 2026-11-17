#!/usr/bin/env bash
# Static checks (CLAUDE.md "Static checks"). Extended by every sprint.
set -u
fail=0
check() { # name, command that must print nothing
  out=$(eval "$2"); if [ -n "$out" ]; then echo "FAIL: $1"; echo "$out"; fail=1; else echo "ok:   $1"; fi
}
check "no pgxpool outside internal/db" \
  "grep -rl pgxpool --include=*.go . | grep -v '^./internal/db/'"
check "no SET SESSION in code or SQL" \
  "grep -rniE 'set[[:space:]]+session' --include=*.go --include=*.sql --include=*.yml --include=Makefile . | grep -v '^./scripts/' | grep -v '^./promts/'"
check "no set_config(..., false)" \
  "grep -rniE \"set_config\\([^)]*,[[:space:]]*false\\)\" --include=*.go --include=*.sql . | grep -v '^./promts/'"
check "compose limits, pins, budget" "python3 scripts/budget.py | grep -E '^(FAIL|  )'"
exit $fail
