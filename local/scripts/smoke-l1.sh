#!/usr/bin/env bash
# Stage L1 behaviour: people sign in with a mailed code, outsiders are refused,
# and the node CA issues machine certificates that verify against it.
set -uo pipefail
cd "$(dirname "$0")/.."
. scripts/lib.sh
fail=0
ok()  { echo "  ok    $1"; }
bad() { echo "  FAIL  $1"; fail=1; }

out=$(curl -s -X POST "$API/auth/send-code" -H 'content-type: application/json' -d '{"email":"someone@example.com"}')
[[ "$out" == *"not allowed"* ]] && ok "an outside address is refused" || bad "an outside address is refused: $out"

token=$(multica_login "smoke@example.com") && [ -n "$token" ] && ok "an example.com address signs in with a mailed code" || bad "sign-in with a mailed code"

scripts/node-ca.sh issue smoke.test >/dev/null
openssl verify -CAfile .state/node-ca/ca.pem .state/machines/smoke.test/agent.pem >/dev/null 2>&1 \
  && ok "a machine certificate verifies against the node CA" || bad "machine certificate verifies"
cert=.state/machines/smoke.test/agent.pem
openssl x509 -in "$cert" -noout -checkend $((89 * 86400)) >/dev/null && ! openssl x509 -in "$cert" -noout -checkend $((91 * 86400)) >/dev/null \
  && ok "the machine certificate lasts 90 days" || bad "the machine certificate lasts 90 days"
rm -rf .state/machines/smoke.test
exit $fail
