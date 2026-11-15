#!/bin/bash
# Re-check on the real cluster the three findings fixed after the first run
# (F-1 completed pods, F-2 services of a namespace without access, F-4 stale
# marker and recovery time). Uses the binary with the fixes.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/verify; rm -rf "$OUT"; mkdir -p "$OUT"; export OUT D=1.5
export BIN=${BIN:-$HOME/m22/bin/huginn-fixed}
export KUBECONFIG=$HOME/m22/kubeconfig
A="kubectl --request-timeout=60s --context gke_huginn-kube-tui_us-east1_huginn-qa"
M=$(dirname "$0")
now() { date +%s; }
# F-1 and F-2 on the owner's and the reader's environments
start rec 200 50; sleep 25; key u; sleep 12; snap F1-rec
start restricted 200 50; sleep 25; key u; sleep 12; snap F2-restricted
key G; key g; key j; snap F2-cursor
# F-4: revoke the metrics rule, wait for the stale mark, restore, time the recovery
t_rev=$(now); $A -n qa-rec patch role huginn-reader --type=json -p '[{"op":"remove","path":"/rules/3"}]' >> "$OUT/log.txt" 2>&1
for i in $(seq 1 40); do tmux capture-pane -p -t "$T" | grep -q 'stale since' && break; sleep 3; done
t_stale=$(now); snap F4-stale
echo "stale after $((t_stale-t_rev)) s" >> "$OUT/log.txt"
sleep 70   # let the backoff reach its cap
$A apply -f $M/m22-rbac.yaml >> "$OUT/log.txt" 2>&1; t_fix=$(now)
for i in $(seq 1 80); do tmux capture-pane -p -t "$T" | grep -q 'stale since' || break; sleep 2; done
t_rec=$(now); snap F4-recovered
echo "automatic recovery $((t_rec-t_fix)) s after the grant was restored" >> "$OUT/log.txt"
# and with r: revoke, wait for stale, restore, press r
$A -n qa-rec patch role huginn-reader --type=json -p '[{"op":"remove","path":"/rules/3"}]' >> "$OUT/log.txt" 2>&1
for i in $(seq 1 40); do tmux capture-pane -p -t "$T" | grep -q 'stale since' && break; sleep 3; done
sleep 70; $A apply -f $M/m22-rbac.yaml >> "$OUT/log.txt" 2>&1; sleep 4
t_r=$(now); tmux send-keys -t "$T" r
for i in $(seq 1 40); do tmux capture-pane -p -t "$T" | grep -q 'stale since' || break; sleep 1; done
echo "recovery with r: $(( $(now)-t_r )) s" >> "$OUT/log.txt"; snap F4-r
