#!/bin/sh
# End-to-end check of Huginn against the real GKE QA cluster (see
# docs/plan/M5-gke-qa.md), modeled on deploy/lab/e2e.sh. Unlike the lab, this
# drives a real cluster with real Spring Boot workloads, so timings are not
# deterministic and the RBAC/KMS wiring must already exist and be applied
# (deploy/gke-qa/workloads.yaml, deploy/gke-qa/rbac.yaml, the huginn-reader
# IAM identity, the KMS key). Run it somewhere tmux is available (native
# Windows has none; use WSL). KUBECONFIG must already have the "rec" (owner)
# and "huginn-reader" (restricted/kms) contexts, and GOOGLE_APPLICATION_CREDENTIALS
# must point at the huginn-reader key so sops can decrypt namespace.env.enc.
# Usage: deploy/gke-qa/e2e.sh [path/to/huginn]   (default: bin/huginn-linux)
set -eu
cd "$(dirname "$0")/../.."
BIN=${1:-bin/huginn-linux}
CFG=deploy/gke-qa/config
FAIL=0

screen() { tmux capture-pane -p -t e2e-gke; }
start() { # start <env>
  # A tmux server, once running, serves new sessions from the *global*
  # environment it captured at server start -- not the caller's env at
  # new-session time. Forward KUBECONFIG/GOOGLE_APPLICATION_CREDENTIALS
  # explicitly so a long-lived server doesn't silently starve sops/kubectl.
  tmux kill-session -t e2e-gke 2>/dev/null || true
  tmux new-session -d -s e2e-gke -x 220 -y 50 \
    "KUBECONFIG=$KUBECONFIG GOOGLE_APPLICATION_CREDENTIALS=$GOOGLE_APPLICATION_CREDENTIALS $BIN --config $CFG $1; sleep 600"
}
# expect <what> <text> [timeout s]: wait until the screen shows text.
expect() {
  i=0
  while [ "$i" -lt "${3:-30}" ]; do
    if screen | grep -qF -- "$2"; then echo "ok   $1"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  echo "FAIL $1: \"$2\" not on screen:"; screen | sed 's/^/     | /'; FAIL=1
}
# expect_re <what> <extended regex> [timeout s]: same, with a pattern.
expect_re() {
  i=0
  while [ "$i" -lt "${3:-30}" ]; do
    if screen | grep -qE -- "$2"; then echo "ok   $1"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  echo "FAIL $1: /$2/ not on screen:"; screen | sed 's/^/     | /'; FAIL=1
}
refuse() { # refuse <what> <text>: the screen must not show text.
  if screen | grep -qF -- "$2"; then echo "FAIL $1: \"$2\" on screen"; screen | sed 's/^/     | /'; FAIL=1; else echo "ok   $1"; fi
}
# keys: one key at a time (two quick escapes would read as alt+escape).
keys() { for k in "$@"; do tmux send-keys -t e2e-gke "$k"; sleep 0.3; done; sleep 1; }

echo "# services (rec)"
start rec
expect "services load" "synced"
expect "real crash loop (unresolvable DB host)" "CrashLoopBackOff"
expect "real OOM kill (heap vs memory limit)" "OOMKilled" 180
expect_re "two ready replicas of one deployment" "payment-service +1 +2/2"

echo "# logs of payment-service"
keys / p a y m e n t Enter Enter
expect "JSON decoded (logger column)" "PaymentWorker"
expect "occasional real error folds into a stack trace" "enter to open" 60
refuse "sidecar hidden by default" "sidecar heartbeat"
keys A
expect "sidecar shown with A" "sidecar heartbeat" 30
expect "container named" "istio-proxy"
keys A
keys 9
expect "head window" "HEAD"
expect "startup in the head" "Started PaymentServiceApplication"
keys Escape Escape

echo "# real JDBC crash: full Hibernate/Hikari stack trace"
keys / c a t a l o g Enter Enter
expect "Hikari pool start" "HikariPool-1"
expect "connection failure logged" "SqlExceptionHelper"
expect "stack trace present" "enter to open"
keys Escape Escape

echo "# restricted identity: one namespace forbidden, the other works"
start restricted
expect "forbidden namespace reported" "forbidden"
expect "allowed namespace listed" "payment-service"

echo "# kms identity: namespace resolved from sops+GCP KMS, not a literal list"
start kms
expect "kms environment loads" "synced"
expect "namespace resolved via sops decrypt" "payment-service"

echo "# no terminal"
if "$BIN" --config "$CFG" rec </dev/null >/dev/null 2>err.txt; then echo "FAIL no-TTY run succeeded"; FAIL=1
elif grep -q "interactive terminal" err.txt; then echo "ok   no terminal"; else echo "FAIL no-TTY message: $(cat err.txt)"; FAIL=1; fi
rm -f err.txt

tmux kill-session -t e2e-gke 2>/dev/null || true
exit $FAIL
