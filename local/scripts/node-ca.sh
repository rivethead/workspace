#!/usr/bin/env bash
# The local node CA: the stand-in for the KMS key alias/workspace-v1/spire-node-ca
# (docs/plan.md, SPIRE). Each laptop's SPIRE agent proves itself with a machine
# certificate (CN <person>.<machine>, 90 days) that this CA signs.
#
#   scripts/node-ca.sh init                   create the CA (once)
#   scripts/node-ca.sh issue <person>.<machine>   key and certificate for one laptop
#
# In the cloud two people sign each certificate with `step` and KMS; here one file key
# signs, so treat .state/ as a secret and never commit it.
set -euo pipefail
cd "$(dirname "$0")/.."
dir=.state/node-ca
mkdir -p "$dir"

case "${1:-}" in
  init)
    if [ -f "$dir/ca.pem" ]; then echo "node CA exists: $dir/ca.pem"; exit 0; fi
    openssl genrsa -out "$dir/ca.key" 3072 2>/dev/null
    openssl req -x509 -new -key "$dir/ca.key" -sha256 -days 365 \
      -subj "/O=example-org/CN=workspace.local node CA" \
      -addext "basicConstraints=critical,CA:true" \
      -addext "keyUsage=critical,keyCertSign,cRLSign" \
      -out "$dir/ca.pem"
    chmod 600 "$dir/ca.key"
    echo "created node CA: $dir/ca.pem"
    ;;
  issue)
    cn="${2:?usage: node-ca.sh issue <person>.<machine>}"
    [[ "$cn" =~ ^[a-z0-9-]+\.[a-z0-9-]+$ ]] || { echo "name must be <person>.<machine>, lower case: $cn" >&2; exit 1; }
    [ -f "$dir/ca.pem" ] || { echo "run: scripts/node-ca.sh init" >&2; exit 1; }
    out=.state/machines/$cn
    mkdir -p "$out"
    openssl genrsa -out "$out/agent.key" 2048 2>/dev/null
    openssl req -new -key "$out/agent.key" -subj "/O=example-org/CN=$cn" -out "$out/agent.csr"
    openssl x509 -req -in "$out/agent.csr" -CA "$dir/ca.pem" -CAkey "$dir/ca.key" -CAcreateserial \
      -days 90 -sha256 -out "$out/agent.pem" \
      -extfile <(printf 'basicConstraints=critical,CA:false\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=clientAuth\n') 2>/dev/null
    rm "$out/agent.csr"
    chmod 600 "$out/agent.key"
    cp "$dir/ca.pem" "$out/node-ca.pem"
    echo "issued $cn: $out/agent.pem (90 days)"
    ;;
  *)
    sed -n '2,11p' "$0" >&2; exit 1 ;;
esac
