#!/bin/bash
# P-2/C-9 error panel (API unreachable), T-2 resize storm, T-1 themes x sizes.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/ui; rm -rf "$OUT"; mkdir -p "$OUT"; export OUT
alive() { tmux has-session -t "$T" 2>/dev/null && [ -n "$(tmux list-panes -t "$T" -F '#{pane_dead}' | grep 0)" ]; }
# 1. API unreachable: a proxy that refuses connections
tmux kill-session -t "$T" 2>/dev/null
tmux new-session -d -s "$T" -x 200 -y 50 "HTTPS_PROXY=http://127.0.0.1:9 BIN=$HOME/m22/bin/huginn-plain $HOME/m22/run.sh rec; sleep 300"
sleep 40; snap 01-unreachable-services
key u; sleep 6; snap 02-unreachable-usage
key r; sleep 8; snap 03-unreachable-retry
tmux kill-session -t "$T"
# 2. resize storm on the usage screen
start rec 200 50; sleep 25; key u; sleep 8
n=0; bad=0
for i in $(seq 1 200); do
  w=$(( (RANDOM % 240) + 10 )); h=$(( (RANDOM % 60) + 3 ))
  tmux resize-window -t "$T" -x $w -y $h 2>/dev/null; sleep 0.04
  alive || { bad=$((bad+1)); echo "dead after ${w}x${h}" >> "$OUT/resize.txt"; break; }
  n=$((n+1))
done
for sz in "20 6" "10 3" "200 50"; do set -- $sz; tmux resize-window -t "$T" -x $1 -y $2; sleep 1.5; snap "04-resize-$1x$2"; done
echo "resizes=$n dead=$bad" >> "$OUT/resize.txt"
tmux kill-session -t "$T"
# 3. themes x sizes on services and usage
for th in light dark accessible classic none auto; do
  for sz in "80 24" "120 30" "220 50"; do set -- $sz
    tmux kill-session -t "$T" 2>/dev/null
    tmux new-session -d -s "$T" -x $1 -y $2 "BIN=$HOME/m22/bin/huginn-plain $HOME/m22/run.sh --theme $th rec; sleep 120"
    sleep 14; snap "10-$th-$1x$2-services"; key u; sleep 6; snap "11-$th-$1x$2-usage"
    alive && echo "$th $1x$2 alive" >> "$OUT/themes.txt" || echo "$th $1x$2 DEAD" >> "$OUT/themes.txt"
  done
done
tmux kill-session -t "$T" 2>/dev/null
echo done > "$OUT/done"
