#!/usr/bin/env bash
# End-to-end smoke test: build the binary, serve a temporary database and
# drive the CLI through sign-up, projects, keywords, drafts and sign-out.
# Offline: nothing here scans or publishes.
set -euo pipefail

cd "$(dirname "$0")/.."
work=$(mktemp -d)
port=${RADARO_E2E_PORT:-18099}
server="http://127.0.0.1:$port"
trap 'kill "${pid:-}" 2>/dev/null || true; rm -rf "$work"' EXIT

export RADARO_CONFIG_DIR="$work/config" RADARO_SERVER="" RADARO_HOME="$work/home"
bin="$work/radaro"
CGO_ENABLED=0 go build -o "$bin" ./cmd/radaro

"$bin" --db "$work/radaro.db" demo --serve=false >/dev/null
"$bin" --db "$work/radaro.db" serve --port "$port" >"$work/serve.log" 2>&1 &
pid=$!
for _ in $(seq 50); do
  curl -fsS "$server/health" >/dev/null 2>&1 && break
  sleep 0.1
done

step() { printf '• %s\n' "$*"; }
fail() { printf '✗ %s\n' "$*" >&2; cat "$work/serve.log" >&2; exit 1; }
expect() { # expect <needle> <command…>: the output must contain needle
  local needle=$1; shift
  local out
  out=$("$@" 2>&1) || fail "$* failed: $out"
  [[ $out == *"$needle"* ]] || fail "$* printed: $out (expected $needle)"
}

step "the API and pages need a session"
[[ $(curl -s -o /dev/null -w '%{http_code}' "$server/api/projects") == 401 ]] || fail "/api/projects without a session"
[[ $(curl -s -o /dev/null -w '%{http_code}' "$server/login") == 200 ]] || fail "/login is not public"

step "register the first user (admin) and take over the demo data"
expect "account created" bash -c "echo 'correct horse' | '$bin' register --server '$server' --email owner@example.com --password-stdin"
expect "owner@example.com (admin)" "$bin" whoami
expect "30" "$bin" project list

step "registration is closed for a second user"
out=$(echo 'correct horse' | "$bin" register --email second@example.com --password-stdin 2>&1) && fail "second sign-up succeeded"
[[ $out == *"registration is closed"* ]] || fail "unexpected error: $out"

step "projects and keywords"
expect "created project Launch (id 2)" "$bin" project create Launch
expect "added 2 keyword(s)" "$bin" project add 2 radaro "social listening"
expect "social listening" "$bin" project keywords 2
expect 'removed "radaro"' "$bin" project remove 2 radaro
expect "is now Launch week" "$bin" project rename 2 "Launch week"
expect "radaro project bind 2 reddit" "$bin" project accounts 2

step "drafts stay behind approval"
expect "draft 1 created" "$bin" draft add --project 2 --platform devto --title Hello --body "First post"
out=$("$bin" publish 1 2>&1) && fail "an unapproved draft was published"
[[ $out == *"not approved"* ]] || fail "unexpected error: $out"
expect "approved" "$bin" draft approve 1
out=$("$bin" publish 1 2>&1) && fail "published without an account"
[[ $out == *"no devto account"* ]] || fail "unexpected error: $out"
expect "draft.approved" "$bin" activity

step "backup while serving"
expect "backed up" "$bin" --db "$work/radaro.db" admin backup "$work/backup.db"
[[ $(stat -c %a "$work/backup.db" 2>/dev/null || stat -f %Lp "$work/backup.db") == 600 ]] || fail "backup is not 0600"

step "sign out, wrong password, sign in"
expect "signed out" "$bin" logout
out=$("$bin" project list 2>&1) && fail "commands work after logout"
out=$(echo 'wrong password' | "$bin" login --email owner@example.com --password-stdin 2>&1) && fail "wrong password accepted"
[[ $out == *"invalid email or password"* ]] || fail "unexpected error: $out"
expect "signed in" bash -c "echo 'correct horse' | '$bin' login --email owner@example.com --password-stdin"
[[ $(stat -c %a "$RADARO_CONFIG_DIR/auth.json" 2>/dev/null || stat -f %Lp "$RADARO_CONFIG_DIR/auth.json") == 600 ]] || fail "auth.json is not 0600"

printf '✓ end-to-end OK\n'
