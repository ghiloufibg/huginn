#!/bin/bash
# N-4 isolated: one kind of cycling, then a long settle, for one binary.
# usage: BIN=... T=session OUT=dir MODE=logs|env m22-drive-leak.sh
. "$(dirname "$0")/m22-drive-lib.sh"
: "${BIN:?}" "${MODE:?}"; export BIN
rm -rf "$OUT"; mkdir -p "$OUT"; PTY=$OUT/pty.raw; : > "$PTY"
start rec 200 50
tmux pipe-pane -t "$T" -o "cat >> $PTY"
bash "$(dirname "$0")/m22-sample.sh" "$T" "$OUT" "$PTY" &
SAMP=$!
phase() { echo -e "$(date +%s)\t$1" >> "$OUT/phases.tsv"; }
sleep 40; phase base
D=1
if [ "$MODE" = follow ]; then
  # the sequence of the first NFR run: a long follow, then back to services
  key /; key r a n g e - e m i t t e r; key Enter; key Enter
  sleep 120; phase peak-follow; key Escape
elif [ "$MODE" = logs ]; then
  key /; key r a n g e - e m i t t e r; key Enter
  for i in $(seq 1 30); do D=4 key Enter; D=1 key Escape; done
else
  for i in $(seq 1 20); do key C-e; key Down; key Enter; sleep 3; key C-e; key Up; key Enter; sleep 3; done
fi
phase after-cycles
sleep 240; phase settled
touch "$OUT/stop"; wait $SAMP
