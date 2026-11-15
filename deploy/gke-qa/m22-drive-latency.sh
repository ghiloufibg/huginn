#!/bin/bash
# N-11 startup, N-12 input-to-redraw latency, C-9/P-2 error panel. 3-5 runs each.
# lat <what> <keys> <expected-text>: ms from send-keys to the first pane poll (50 ms) showing the text.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/latency; rm -rf "$OUT"; mkdir -p "$OUT"; RES=$OUT/results.tsv
now() { date +%s%N; }
wait_text() { # wait_text <text> [timeout_ms] -> echoes ms or TIMEOUT
  local t0=$(now) lim=${2:-20000}
  while :; do
    tmux capture-pane -p -t "$T" | grep -qF -- "$1" && { echo $(( ($(now)-t0)/1000000 )); return; }
    [ $(( ($(now)-t0)/1000000 )) -gt "$lim" ] && { echo TIMEOUT; return; }
    sleep 0.05
  done
}
lat() { local what=$1 keys=$2 text=$3 t0=$(now); tmux send-keys -t "$T" $keys; local r=$(wait_text "$text" 15000)
        echo -e "$what\t$r" >> "$RES"; }
# startup: from new-session to "synced" in the header (services), 3 runs
for i in 1 2 3; do
  tmux kill-session -t "$T" 2>/dev/null; t0=$(now)
  tmux new-session -d -s "$T" -x 200 -y 50 "BIN=$HOME/m22/bin/huginn-plain $HOME/m22/run.sh rec; sleep 900"
  r=$(wait_text "synced" 60000); echo -e "startup-to-synced\t$r" >> "$RES"
  r=$(wait_text "deploy-auto" 5000); echo -e "startup-to-first-row\t$r" >> "$RES"
done
sleep 5
for i in 1 2 3 4 5; do
  lat usage-open        "u"      "NAMESPACE / SERVICE"; sleep 0.3
  wait_text "sampled" 15000 >/dev/null
  lat usage-expand      "j Enter" "usage-mem"            ; sleep 0.5
  lat usage-sort        "s"      "sort cpu"              ; sleep 0.5
  lat usage-sort-back   "s s s"  "sort memory"           ; sleep 0.5
  lat usage-filter-open "/"      "filter"                ; tmux send-keys -t "$T" Escape; sleep 0.5
  lat usage-detail      "p"      "requests"              ; sleep 0.5; tmux send-keys -t "$T" p
  lat usage-back        "Escape" "REPO"                  ; sleep 0.5
  lat services-down     "j"      "deploy-cron"           ; tmux send-keys -t "$T" k; sleep 0.5
done
echo done > "$OUT/done"
