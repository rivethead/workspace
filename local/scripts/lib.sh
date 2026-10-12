# Shared helpers for the local scripts. Source it; do not run it.
# shellcheck shell=bash
set -a; [ -f "$(dirname "${BASH_SOURCE[0]}")/../.env" ] && . "$(dirname "${BASH_SOURCE[0]}")/../.env"; set +a
API="http://localhost:${MULTICA_API_PORT:-8080}"
MAILPIT="http://localhost:${MAILPIT_PORT:-8025}"

# multica_login <email>: sign in with a code from Mailpit; prints the session token.
multica_login() {
  local email="$1" id code
  curl -fsS -X DELETE "$MAILPIT/api/v1/messages" >/dev/null
  # Multica allows 5 sign-in codes a minute from one address; wait out a 429.
  local http_status
  for _ in $(seq 6); do
    http_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$API/auth/send-code" -H 'content-type: application/json' -d "{\"email\":\"$email\"}")
    [ "$http_status" != 429 ] && break
    echo "sign-in rate limit; waiting 15s" >&2; sleep 15
  done
  [ "$http_status" = 200 ] || { echo "send-code for $email returned $http_status" >&2; return 1; }
  for _ in $(seq 20); do
    id=$(curl -fsS "$MAILPIT/api/v1/search?query=to:$email" | python3 -c "import sys,json;m=json.load(sys.stdin)['messages'];print(m[0]['ID'] if m else '')")
    [ -n "$id" ] && break; sleep 0.5
  done
  [ -n "$id" ] || { echo "no sign-in mail for $email" >&2; return 1; }
  code=$(curl -fsS "$MAILPIT/api/v1/message/$id" | python3 -c "import sys,json,re;print(re.search(r'\b(\d{6})\b',json.load(sys.stdin)['Text']).group(1))")
  curl -fsS -X POST "$API/auth/verify-code" -H 'content-type: application/json' -d "{\"email\":\"$email\",\"code\":\"$code\"}" \
    | python3 -c "import sys,json;print(json.load(sys.stdin)['token'])"
}

# multica_session <email>: a session token for <email>, reused from .state/sessions
# while it still works, so the scripts stay under Multica's sign-in rate limit.
multica_session() {
  local email="$1" dir file token
  dir="$(dirname "${BASH_SOURCE[0]}")/../.state/sessions"; file="$dir/$email"
  mkdir -p "$dir"
  if [ -s "$file" ] && curl -fsS -o /dev/null "$API/api/workspaces/" -H "Authorization: Bearer $(cat "$file")"; then
    cat "$file"; return
  fi
  token=$(multica_login "$email") || return 1
  (umask 077; printf '%s' "$token" > "$file")
  printf '%s' "$token"
}

# mc <method> <path> [json]: call the Multica API with $MC_TOKEN, in the workspace
# $MULTICA_WORKSPACE_ID when it is set.
mc() {
  curl -fsS -X "$1" "$API$2" -H "Authorization: Bearer $MC_TOKEN" -H 'content-type: application/json' \
    ${MULTICA_WORKSPACE_ID:+-H "X-Workspace-ID: $MULTICA_WORKSPACE_ID"} ${3:+-d "$3"}
}
