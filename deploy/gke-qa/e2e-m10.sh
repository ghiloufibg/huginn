#!/bin/sh
# M10 delta over M5's e2e.sh: real-GKE checks for M6-M9 features never run
# against a real cluster before (docs/plan/M10-gke-qa.md §0/§7). Does not
# repeat anything deploy/gke-qa/e2e.sh (M5) already proved. Run after
# workloads.yaml (payment-service-loadgen, noisy-fixture included) and
# config (formats/30-noisy.yaml, ui.yaml redact/save/clipboard) are applied.
# Usage: deploy/gke-qa/e2e-m10.sh [path/to/huginn]   (default: bin/huginn-linux)
set -eu
cd "$(dirname "$0")/../.."
BIN=${1:-bin/huginn-linux}
CFG=deploy/gke-qa/config
SAVE_DIR="$HOME/huginn-gke-qa-saves"
FAIL=0

screen() { tmux capture-pane -p -t e2e-m10; }
start() { # start <env>
  tmux kill-session -t e2e-m10 2>/dev/null || true
  tmux new-session -d -s e2e-m10 -x 220 -y 50 \
    "KUBECONFIG=$KUBECONFIG $BIN --config $CFG $1; sleep 600"
}
expect() {
  i=0
  while [ "$i" -lt "${3:-30}" ]; do
    if screen | grep -qF -- "$2"; then echo "ok   $1"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  echo "FAIL $1: \"$2\" not on screen:"; screen | sed 's/^/     | /'; FAIL=1
}
refuse() {
  if screen | grep -qF -- "$2"; then echo "FAIL $1: \"$2\" on screen"; screen | sed 's/^/     | /'; FAIL=1; else echo "ok   $1"; fi
}
keys() { for k in "$@"; do tmux send-keys -t e2e-m10 "$k"; sleep 0.3; done; sleep 1; }

echo "# services (rec) sanity after rebuild"
start rec
expect "services load" "synced"
expect_re() {
  i=0
  while [ "$i" -lt "${3:-30}" ]; do
    if screen | grep -qE -- "$2"; then echo "ok   $1"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  echo "FAIL $1: /$2/ not on screen:"; screen | sed 's/^/     | /'; FAIL=1
}
expect_re "payment-service-loadgen listed as its own repo" "payment-service-loadgen"
expect_re "noisy-fixture listed as its own repo" "noisy-fixture"

echo "# M8.2: trace view against payment-service's real trace_id"
keys / p a y m e n t Enter Enter
expect "logs open" "PaymentWorker"
keys v
expect "trace view opens" "trace" 15
keys Escape

echo "# M8.1: field filter from zoom"
keys Enter
expect "zoom opens" "enter to open" 10
keys Tab
keys "="
expect "filter applied, status bar reflects it" "filter" 15
keys Escape Escape

echo "# M9.1/M9.2: select, copy, save, redact (noisy-fixture)"
keys Escape
keys / n o i s y Enter Enter
expect "noisy-fixture logs open" "Catalog page viewed" 30
expect "secret visible on screen (never redacted on screen)" "alice.debug@example.invalid" 20
keys V
keys j j
keys V
keys "y"
keys "ctrl+s"
sleep 2
LATEST=$(ls -t "$SAVE_DIR" 2>/dev/null | head -1 || true)
if [ -n "$LATEST" ] && [ -f "$SAVE_DIR/$LATEST" ]; then
  echo "ok   save wrote a file: $LATEST"
  if grep -q "alice.debug@example.invalid" "$SAVE_DIR/$LATEST"; then
    echo "FAIL redact: secret present unredacted in saved file"; FAIL=1
  else
    echo "ok   redact: raw email absent from saved file"
  fi
  if grep -q "redacted" "$SAVE_DIR/$LATEST"; then
    echo "ok   redact: [redacted] marker present in saved file"
  else
    echo "FAIL redact: no [redacted] marker in saved file"; FAIL=1
  fi
else
  echo "FAIL save: no file appeared in $SAVE_DIR"; FAIL=1
fi
keys Escape

echo "# M7: level_from on noisy-fixture's non-standard 'sev' field"
expect "warn line raised to WARN" "WARN" 15
expect "error line raised to ERROR" "ERROR" 15

echo "# M6.1/M6.2: pair_pattern extraction (key: \"value\"; syntax)"
keys Enter
expect "zoom shows an extracted pair field" "user" 15
keys Escape

keys Escape Escape
tmux kill-session -t e2e-m10 2>/dev/null || true
exit $FAIL
