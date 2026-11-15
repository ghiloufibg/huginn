#!/bin/bash
# N-5: 60-minute soak alone on the cluster: services -> usage -> logs follow -> Kafka records, every ~5.5 min.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/soak; rm -rf "$OUT"; mkdir -p "$OUT"; export OUT D=1
export BIN=$HOME/m22/bin/huginn-plain
start rec 200 50
bash "$(dirname "$0")/m22-sample.sh" "$T" "$OUT" &
SAMP=$!
sleep 30
phase() { echo -e "$(date +%s)\t$1" >> "$OUT/phases.tsv"; }
for c in $(seq 1 11); do
  phase "cycle-$c-services"; sleep 60
  key u; sleep 120; key Escape
  key / r a n g e - e m i t t e r; key Enter; key Enter; sleep 90; key Escape; key Escape
  key / c a t a l o g; key Enter; key M; sleep 5; key Enter; sleep 30; key Escape; key Escape; key Escape
  snap "cycle-$c"
  tmux has-session -t "$T" 2>/dev/null && echo "$c alive" >> "$OUT/alive.txt" || { echo "$c DEAD" >> "$OUT/alive.txt"; break; }
done
phase end; touch "$OUT/stop"; wait $SAMP
