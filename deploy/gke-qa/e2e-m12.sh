#!/bin/sh
# M12 delta over M10's e2e-m10.sh: real-GKE checks for the Kafka feature
# (docs/plan/M12-gke-qa.md, M11-kafka.md), never run against a real broker
# before this round. Scripts the checks run ad hoc during the M12 session
# (see deploy/gke-qa/M12-QA-REPORT.md) so a future round 4 doesn't have to
# rediscover the same tmux/path/sops gotchas.
#
# Prerequisites (one-time, see deploy/gke-qa/wsl-setup.sh):
#   - run from WSL, not Git Bash (tmux and the Linux huginn binary need it)
#   - sops and age on PATH, SOPS_AGE_KEY_FILE set to the session's age.key
#   - kubectl port-forward -n qa-rec svc/kafka-qa 9092:9092 9094:9094 running
#   - deploy/gke-qa/kafka-workloads.yaml, kafka-orders-fixture.yaml,
#     kafka-catalog-fixture.yaml applied; topics and the ACL on
#     topic-restricted created (deploy/gke-qa/M12-QA-REPORT.md's setup
#     section has the exact commands -- not re-scripted here since they
#     involve real, session-specific secrets under kafka-secrets/, which
#     is gitignored and regenerated fresh each round)
#
# Usage: deploy/gke-qa/e2e-m12.sh [path/to/huginn]   (default: bin/huginn-linux)
set -eu
cd "$(dirname "$0")/../.."
BIN=${1:-bin/huginn-linux}
CFG=deploy/gke-qa/config-kafka
FAIL=0

screen() { tmux capture-pane -p -t e2e-m12; }
start() { # start <env>
  tmux kill-session -t e2e-m12 2>/dev/null || true
  tmux new-session -d -s e2e-m12 -x 220 -y 50 \
    "KUBECONFIG=$KUBECONFIG SOPS_AGE_KEY_FILE=$SOPS_AGE_KEY_FILE $BIN --config $CFG $1; sleep 600"
}
expect() {
  i=0
  while [ "$i" -lt "${3:-30}" ]; do
    if screen | grep -qF -- "$2"; then echo "ok   $1"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  echo "FAIL $1: \"$2\" not on screen:"; screen | sed 's/^/     | /'; FAIL=1
}
keys() { for k in "$@"; do tmux send-keys -t e2e-m12 "$k"; sleep 0.3; done; sleep 1; }

echo "# CLI: no TUI, no tmux needed"
if "$BIN" kafka check catalog --config "$CFG" -e rec 2>&1 | grep -q "^catalog in rec"; then
  echo "ok   kafka check catalog (plaintext tier)"
else
  echo "FAIL kafka check catalog"; FAIL=1
fi
if "$BIN" kafka check orders --config "$CFG" -e rec 2>&1 | grep -q "not authorized: forbidden"; then
  echo "ok   kafka check orders: topic-restricted correctly forbidden"
else
  echo "FAIL kafka check orders: expected not authorized on topic-restricted"; FAIL=1
fi
READ_OUT=$("$BIN" kafka read orders topic-a --config "$CFG" -e rec --tail 10 2>&1)
for shape in 'key=∅' 'tombstone' 'binary 9 B' 'schema 7, 12 B'; do
  if echo "$READ_OUT" | grep -qF -- "$shape"; then echo "ok   kafka read: $shape"; else echo "FAIL kafka read: missing $shape"; FAIL=1; fi
done

echo "# services screen: K marker"
start rec
expect "services load" "synced"
expect "orders repo shows K marker" "orders K" 20
expect "catalog repo shows K marker" "catalog K" 5

echo "# M8-style Kafka screen: CONSUMES/PRODUCES/TOPICS grouping"
keys /
keys o r d e r s Enter Enter
keys M
expect "orders Kafka screen opens" "CONSUMES" 15
expect "topic-restricted shows its forbidden reason inline" "not authorized" 10

echo "# records, zoom, filter, order, isolation, copy"
keys Enter
expect "topic-a records open" "schema 7, 12 B" 10
keys / k e y "=" k "-" 1 Enter
expect "key= filter narrows the view" "k-1" 10
keys Escape
keys o
expect "order toggle (newest first)" "bin-key-2" 10
keys i
expect "isolation toggle" "committed" 10
keys y
expect "copy confirms in the status bar" "copied" 10

keys Escape Escape Escape
tmux kill-session -t e2e-m12 2>/dev/null || true
exit $FAIL
