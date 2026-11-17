#!/usr/bin/env bash
# Creates deploy/.env from the example, or appends keys added to the example
# since (never overwrites existing values).
set -euo pipefail
ex=deploy/.env.example; env=deploy/.env
if [ ! -f "$env" ]; then cp "$ex" "$env"; echo "created $env"; exit 0; fi
while IFS= read -r line; do
  key="${line%%=*}"
  case "$line" in ''|\#*|*[!A-Z0-9_=]*=*) ;; esac
  [[ "$line" =~ ^[A-Z0-9_]+= ]] || continue
  grep -q "^${key}=" "$env" || { echo "$line" >> "$env"; echo "added $key to $env"; }
done < "$ex"
