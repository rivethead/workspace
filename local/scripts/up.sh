#!/usr/bin/env bash
# Start the local workspace. Creates .env with fresh secrets and the node CA on
# the first run; safe to re-run.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ ! -f .env ]; then
  sed "s/^JWT_SECRET=.*/JWT_SECRET=$(openssl rand -hex 32)/" .env.example > .env
  echo "created .env"
fi
scripts/node-ca.sh init
# The broker's rules (the provisioner writes them from the registry in the cloud).
# They start empty, so the broker refuses every agent until a person adds one.
if [ ! -f .state/broker/rules.json ]; then
  mkdir -p .state/broker .state/github
  echo '{"agents": {}}' > .state/broker/rules.json
  echo "created .state/broker/rules.json (no agents yet)"
fi
docker compose up -d --wait
scripts/check.sh
