#!/bin/bash
# R-3/R-4/U-18: the usage screen while the cluster changes under it (reader identity).
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/chaos; rm -rf "$OUT"; mkdir -p "$OUT"; export OUT
export KUBECONFIG=$HOME/m22/kubeconfig
A="kubectl --request-timeout=60s --context gke_huginn-kube-tui_us-east1_huginn-qa"
M=/mnt/c/Users/PC/IdeaProjects/huginn/deploy/gke-qa
start restricted 200 50; sleep 30
key u; sleep 12; snap A-before
echo "$(date +%T) revoke metrics grant" >> "$OUT/log.txt"
$A -n qa-rec patch role huginn-reader --type=json -p '[{"op":"remove","path":"/rules/3"}]' >> "$OUT/log.txt" 2>&1
sleep 45; snap B-grant-revoked
echo "$(date +%T) restore grant" >> "$OUT/log.txt"
$A apply -f $M/m22-rbac.yaml >> "$OUT/log.txt" 2>&1
sleep 45; snap C-grant-restored
echo "$(date +%T) scale usage-mem to 0" >> "$OUT/log.txt"
$A -n qa-rec scale deploy usage-mem --replicas=0 >> "$OUT/log.txt" 2>&1
sleep 45; snap D-scaled-to-0
echo "$(date +%T) scale usage-mem to 3" >> "$OUT/log.txt"
$A -n qa-rec scale deploy usage-mem --replicas=3 >> "$OUT/log.txt" 2>&1
sleep 25; snap E-scaling-up
sleep 60; snap F-scaled-up
key Escape; sleep 2
# R-3: follow range-emitter, delete the followed pod
key / r a n g e - e m i t t e r; key Enter; key Enter; sleep 10; snap G-logs-before-delete
echo "$(date +%T) delete range-emitter-0" >> "$OUT/log.txt"
$A -n qa-rec delete pod range-emitter-0 --wait=false >> "$OUT/log.txt" 2>&1
sleep 40; snap H-logs-after-delete
tmux has-session -t "$T" && echo "session alive" >> "$OUT/log.txt"
