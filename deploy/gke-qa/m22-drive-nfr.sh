#!/bin/bash
# N-1, N-4, N-6: footprint per screen, flicker bytes, leak test by cycling.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/nfr; rm -rf "$OUT"; mkdir -p "$OUT"
PTY=$OUT/pty.raw; : > "$PTY"
BIN=${BIN:-$HOME/m22/bin/huginn-plain}
export BIN
start rec 200 50
tmux pipe-pane -t "$T" -o "cat >> $PTY"
bash "$(dirname "$0")/m22-sample.sh" "$T" "$OUT" "$PTY" &
SAMP=$!
phase() { echo -e "$(date +%s)\t$1\t$(stat -c %s "$PTY")" >> "$OUT/phases.tsv"; }
sleep 30; phase services-idle-start
sleep 120; phase usage-idle-start
D=1; key u; sleep 150; phase logs-follow-start
key Escape; key / ; key r a n g e - e m i t t e r; key Enter; key Enter; sleep 120; phase cycle-usage-start
key Escape; key Escape; sleep 3
D=0.35
for i in $(seq 1 100); do key u; key Escape; done; phase cycle-logs-start
for i in $(seq 1 50); do key Enter; key Escape; done; phase cycle-env-start
for i in $(seq 1 20); do key C-e; key Down; key Enter; sleep 2; key C-e; key Up; key Enter; sleep 2; done; phase settle-start
sleep 60; phase end
touch "$OUT/stop"; wait $SAMP
