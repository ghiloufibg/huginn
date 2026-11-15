#!/bin/bash
# M22 QA driver helpers (docs/plan/M22-gke-full-qa.md §5). Source this file.
# key <keys...>   send keys to the tmux session $T and wait $D seconds
# snap <name>     capture the pane to $OUT/<name>.txt
T=${T:-u}; OUT=${OUT:-$HOME/m22/out}; D=${D:-1.5}
mkdir -p "$OUT"
key()  { tmux send-keys -t "$T" "$@"; sleep "$D"; }
snap() { tmux capture-pane -p -t "$T" > "$OUT/$1.txt"; }
start() { # start <env> [cols rows]
  tmux kill-session -t "$T" 2>/dev/null || true
  tmux new-session -d -s "$T" -x "${2:-200}" -y "${3:-50}" "BIN=\"$BIN\" GOCOVERDIR=\"${GOCOVERDIR:-}\" $HOME/m22/run.sh $1; sleep 900"
}
