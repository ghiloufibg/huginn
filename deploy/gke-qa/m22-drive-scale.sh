#!/bin/bash
# M22 scale run: 20 namespaces x 3 services (m22-scale-fixture.sh), usage screen
# opened, expanded, sorted, filtered, scrolled. Samples RSS/threads/fds/bytes.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/scale; rm -rf "$OUT"; mkdir -p "$OUT"
PTY=$OUT/pty.raw; : > "$PTY"
BIN=${BIN:-$HOME/m22/bin/huginn-plain}; export BIN
start scale 200 50
tmux pipe-pane -t "$T" -o "cat >> $PTY"
bash "$(dirname "$0")/m22-sample.sh" "$T" "$OUT" "$PTY" &
SAMP=$!
phase() { echo -e "$(date +%s)\t$1\t$(stat -c %s "$PTY")" >> "$OUT/phases.tsv"; }
sleep 20; snap 01-services; phase services-idle
D=2; key u; sleep 25; snap 02-usage-collapsed; phase usage-open
for i in $(seq 1 12); do key Down; done; snap 03-scrolled
key Enter; snap 04-expanded
D=1; key s; snap 05-sort1; key s; snap 06-sort2
key / ; key q a - s 1; key Enter; snap 07-filter; key Escape
D=0.2; for i in $(seq 1 60); do key Down; done; snap 08-bottom
for i in $(seq 1 60); do key Up; done; phase navigated
sleep 120; snap 09-after-2min; phase usage-idle-2min
key Escape; sleep 5; snap 10-back; phase end
touch "$OUT/stop"; wait $SAMP
