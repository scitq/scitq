#!/usr/bin/env bash
# Upgrade test (docs/reference/compatibility.md): a server and worker of the
# previous release run a workflow; the server is replaced by the candidate
# mid-workflow, its migrations run under a live worker, the remaining tasks
# finish, the worker is flagged needs_upgrade, then upgraded in place.
#
# Usage: tests/upgrade/run.sh <base-tag>      (make upgrade-test)
# Needs: git, go, docker (PostgreSQL), python3 with venv, jq, curl.
# The worker upgrade step runs on linux/amd64 only, the architecture scitq
# applies upgrades on.
set -euo pipefail
unset SCITQ_SERVER SCITQ_TOKEN # never act on a server from the caller's shell

BASE=${1:?usage: $0 <base-tag>}
ROOT=$(git rev-parse --show-toplevel)
WORK=$(mktemp -d)
PG_NAME="scitq-upgrade-pg-$$"
SERVER_PID="" WORKER_PID=""

FAILED=""

say()  { echo "▶ $*"; }
fail() {
  FAILED=1
  echo "❌ $*"
  for f in "$WORK"/server-*.log "$WORK"/worker.log; do
    [ -f "$f" ] && { echo "----- tail $f"; tail -40 "$f"; }
  done
  echo "Full logs kept in $WORK"
  exit 1
}
cleanup() {
  [ -n "$WORKER_PID" ] && kill "$WORKER_PID" 2>/dev/null || true
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  docker rm -f "$PG_NAME" >/dev/null 2>&1 || true
  git -C "$ROOT" worktree remove --force "$WORK/base-src" >/dev/null 2>&1 || true
  [ -n "$FAILED" ] || rm -rf "$WORK"
}
trap cleanup EXIT

wait_for() { # wait_for <seconds> <description> <condition function> [args...]
  local deadline=$((SECONDS + $1)) what=$2; shift 2
  until "$@" >/dev/null 2>&1; do
    [ $SECONDS -ge $deadline ] && fail "timed out waiting for $what"
    sleep 2
  done
}
free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'; }

# --- 1. Build both versions -------------------------------------------------
say "building $BASE (previous release) and the candidate"
git -C "$ROOT" worktree add --detach "$WORK/base-src" "$BASE" >/dev/null
make -C "$WORK/base-src" BINARY_DIR="$WORK/base" build-server build-client build-cli >"$WORK/build-base.log" 2>&1 || fail "base build failed (see $WORK/build-base.log)"
make -C "$ROOT" BINARY_DIR="$WORK/cand" build-server build-client build-cli >"$WORK/build-cand.log" 2>&1 || fail "candidate build failed"
echo "  base:      $("$WORK/base/scitq-server" -version)"
echo "  candidate: $("$WORK/cand/scitq-server" -version)"

# --- 2. Database and configuration -----------------------------------------
PG_PORT=$(free_port) GRPC_PORT=$(free_port) HTTP_PORT=$(free_port)
docker run -d --name "$PG_NAME" -e POSTGRES_USER=test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=scitq \
  -p "127.0.0.1:$PG_PORT:5432" postgres:17-alpine >/dev/null
wait_for 60 "PostgreSQL" docker exec "$PG_NAME" pg_isready -U test -d scitq

ADMIN_PW="pw-$$"
ADMIN_HASH=$("$WORK/cand/scitq" hashpassword "$ADMIN_PW" | grep -oE '\$2[aby]\$[^[:space:]]+')
[ -n "$ADMIN_HASH" ] || fail "could not hash the admin password"
WORKER_TOKEN="upgrade-worker-$$"
mkdir -p "$WORK"/{logs,scripts,modules,venv,store,out}
cp "$WORK/cand/scitq-client" "$WORK/client-to-serve" # what the server hands to upgrading workers
cat >"$WORK/scitq.yaml" <<EOF
scitq:
  port: $GRPC_PORT
  http_port: $HTTP_PORT
  db_url: postgres://test:test@127.0.0.1:$PG_PORT/scitq?sslmode=disable
  server_fqdn: localhost
  log_level: info
  log_root: $WORK/logs
  script_root: $WORK/scripts
  modules_root: $WORK/modules
  script_venv: $WORK/venv
  worker_token: $WORKER_TOKEN
  jwt_secret: upgrade-test-secret
  admin_user: admin
  admin_hashed_password: '$ADMIN_HASH'
  disable_https: true
  disable_grpcweb: true
  client_binary_path: $WORK/client-to-serve
EOF

start_server() { # start_server <base|cand>
  "$WORK/$1/scitq-server" -config "$WORK/scitq.yaml" >>"$WORK/server-$1.log" 2>&1 &
  SERVER_PID=$!
  wait_for 120 "$1 server" port_open "$GRPC_PORT"
}
CLI="$WORK/cand/scitq"
scitq() { SCITQ_NO_VERSION_CHECK=1 "$CLI" --server "localhost:$GRPC_PORT" --token "$TOKEN" "$@"; }
start_worker() {
  "$WORK/worker/scitq-client" -server "localhost:$GRPC_PORT" -token "$WORKER_TOKEN" -name upgrade-worker \
    -store "$WORK/store" -concurrency 1 -permanent -swap 0 >>"$WORK/worker.log" 2>&1 &
  WORKER_PID=$!
}
worker_field() { scitq --json worker list | jq -r ".[] | select(.name==\"upgrade-worker\") | .$1 // empty"; }
task_count() { scitq --json task list --workflow-id "$WF" --status "$1" --limit 100 | jq length; }

# Conditions for wait_for, evaluated in this shell so they see the helpers.
port_open()        { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
worker_registered() { [ -n "$(worker_field worker_id)" ]; }
worker_status_is() { [ "$(worker_field upgrade_status)" = "$1" ]; }
succeeded_at_least() { [ "$(task_count S)" -ge "$1" ]; }

# --- 3. Previous release: server, worker, workflow -------------------------
say "starting the $BASE server and worker"
start_server base
TOKEN=$(SCITQ_NO_VERSION_CHECK=1 "$CLI" --server "localhost:$GRPC_PORT" --json login --user admin --password "$ADMIN_PW" | jq -r .token)
[ -n "$TOKEN" ] || fail "login failed"
mkdir -p "$WORK/worker" && cp "$WORK/base/scitq-client" "$WORK/worker/scitq-client"
start_worker
wait_for 60 "worker registration" worker_registered
WORKER_ID=$(worker_field worker_id)

WF=$(scitq workflow create --name upgrade-test | grep -oE 'ID[^0-9]*[0-9]+' | grep -oE '[0-9]+$')
STEP=$(scitq step create --workflow-id "$WF" --name work | grep -oE 'ID[^0-9]*[0-9]+' | grep -oE '[0-9]+$')
[ -n "$WF" ] && [ -n "$STEP" ] || fail "could not create workflow/step"
scitq worker update --worker-id "$WORKER_ID" --step-id "$STEP" >/dev/null
for i in 1 2 3 4; do
  scitq task create --step-id "$STEP" --container bare --shell sh --name "t$i" \
    --command "sleep 15 && echo done-$i > \"\$OUTPUT/out.txt\"" --output "$WORK/out/t$i/" >/dev/null
done
scitq workflow update --id "$WF" --status R >/dev/null
wait_for 120 "the first task to succeed on $BASE" succeeded_at_least 1
say "$(task_count S)/4 tasks done on $BASE — switching the server"

# --- 4. Candidate server on the same database, worker still running --------
kill -USR1 "$SERVER_PID" # graceful gate: finish in-flight admin jobs, exit 75
code=0; wait "$SERVER_PID" || code=$?
[ "$code" = 75 ] || fail "graceful gate exited with $code, expected 75"
start_server cand
wait_for 300 "all tasks to succeed after the server upgrade" succeeded_at_least 4
[ "$(task_count F)" -eq 0 ] || fail "tasks failed across the server upgrade"
for i in 1 2 3 4; do [ -s "$WORK/out/t$i/out.txt" ] || fail "output of t$i missing"; done
say "all 4 tasks succeeded with a $BASE worker on the candidate server"

# --- 5. Worker upgrade (linux/amd64 only) -----------------------------------
# The server flags a stale worker needs_upgrade only on linux/amd64, the
# architecture it can upgrade; elsewhere it reports unsupported_arch.
if [ "$(uname -s)/$(uname -m)" != "Linux/x86_64" ]; then
  wait_for 60 "unsupported_arch status" worker_status_is unsupported_arch
  say "worker reported unsupported_arch: upgrade step skipped on $(uname -s)/$(uname -m)"
  echo "✓ upgrade test passed ($BASE → candidate server; worker upgrade runs on linux/amd64 only)"
  exit 0
fi
wait_for 60 "needs_upgrade flag" worker_status_is needs_upgrade
say "worker flagged needs_upgrade"
scitq worker upgrade "$WORKER_ID" --yes >/dev/null
code=0; wait "$WORKER_PID" || code=$?
[ "$code" = 75 ] || fail "worker exited with $code during upgrade, expected 75"
cmp -s "$WORK/worker/scitq-client" "$WORK/cand/scitq-client" || fail "worker binary was not replaced by the candidate's"
start_worker # what systemd's Restart=on-failure does in production
wait_for 120 "upgraded worker to report up_to_date" worker_status_is up_to_date

# All four tasks succeeded, so the workflow is finished (S); reopen it, as an
# operator does when adding work, so the new task is dispatched.
scitq workflow update --id "$WF" --status R >/dev/null
scitq task create --step-id "$STEP" --container bare --shell sh --name t5 \
  --command "echo done-5 > \"\$OUTPUT/out.txt\"" --output "$WORK/out/t5/" >/dev/null
wait_for 120 "a task on the upgraded worker" succeeded_at_least 5
echo "✓ upgrade test passed: $BASE → candidate, server and worker"
