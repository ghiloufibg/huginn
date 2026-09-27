#!/bin/sh
# End-to-end check of Huginn on the lab (after up.sh): runs the binary in a
# terminal (tmux) and looks for what each screen must show.
# Usage: deploy/lab/e2e.sh [path/to/huginn]   (default: bin/huginn)
set -eu
cd "$(dirname "$0")/../.."
BIN=${1:-bin/huginn}
CFG=deploy/lab/config
FAIL=0

screen() { tmux capture-pane -p -t e2e; }
start() { # start <env>
  tmux kill-session -t e2e 2>/dev/null || true
  tmux new-session -d -s e2e -x 180 -y 40 "$BIN --config $CFG $1; sleep 600"
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
refuse() { # refuse <what> <text>: the screen must not show text.
  if screen | grep -qF -- "$2"; then echo "FAIL $1: \"$2\" on screen"; screen | sed 's/^/     | /'; FAIL=1; else echo "ok   $1"; fi
}
# keys: one key at a time (two quick escapes would read as alt+escape).
keys() { for k in "$@"; do tmux send-keys -t e2e "$k"; sleep 0.3; done; sleep 1; }

echo "# services (rec)"
start rec
expect "services load" "synced"
expect "crash loop shown" "catalog-indexer" 
expect "image pull failure" "ImagePullBackOff"
expect "unschedulable pod" "Insufficient memory"
expect "OOM kill" "OOMKilled" 120
expect "two workloads of one repository" "payment-service      2"

echo "# logs of payment-service"
keys / p a y m e n t Enter Enter
expect "JSON decoded (logger column)" "PaymentController"
expect "stack trace folded" "enter to open"
refuse "sidecar hidden" "envoy"
keys Escape Escape

echo "# crash loop: waiting, previous instance"
keys / c a t a l o g Enter Enter
expect "waiting container is not an error" "waiting:"
refuse "no reconnect loop" "reconnecting"
keys P
expect "previous instance" "PREVIOUS INSTANCE"
expect "previous instance lines" "elasticsearch"

echo "# rollout seen live"
keys P Escape Escape
kubectl --context kind-huginn -n app-rec rollout restart deploy/payment-worker >/dev/null
expect "rollout in progress" "Progressing" 60

echo "# restricted identity: one namespace forbidden, the other works"
start restricted
expect "forbidden namespace reported" "forbidden"
expect "allowed namespace listed" "payment-service"

echo "# no terminal"
if "$BIN" --config "$CFG" rec </dev/null >/dev/null 2>err.txt; then echo "FAIL no-TTY run succeeded"; FAIL=1
elif grep -q "interactive terminal" err.txt; then echo "ok   no terminal"; else echo "FAIL no-TTY message: $(cat err.txt)"; FAIL=1; fi
rm -f err.txt

tmux kill-session -t e2e 2>/dev/null || true
exit $FAIL
