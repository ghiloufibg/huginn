#!/bin/bash
# R-5: other clients hammer the API server (pods lists and the Metrics API) while
# the usage and logs screens are open. Expected: no error panel, no crash, values keep refreshing.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/load; rm -rf "$OUT"; mkdir -p "$OUT"; export OUT D=1.5
export BIN=$HOME/m22/bin/huginn-fixed KUBECONFIG=$HOME/m22/kubeconfig
A="kubectl --request-timeout=30s --context gke_huginn-kube-tui_us-east1_huginn-qa"
start rec 200 50; sleep 25; key u; sleep 20; snap 0-before
echo "sampled: $(grep -o 'sampled [0-9a-z]* ago' $OUT/0-before.txt)" >> "$OUT/log.txt"
# 12 parallel clients, 90 s: list pods (json) and the metrics API in a loop
for n in $(seq 1 12); do
  ( end=$(( $(date +%s) + 90 )); while [ $(date +%s) -lt $end ]; do
      $A get pods -A -o json >/dev/null 2>&1; $A get --raw /apis/metrics.k8s.io/v1beta1/namespaces/qa-rec/pods >/dev/null 2>&1
      echo x >> "$OUT/reqs"; done ) &
done
sleep 30; snap 1-load-30s; sleep 40; snap 2-load-70s; wait
sleep 25; snap 3-after
echo "requests issued by the other clients: $(wc -l < "$OUT/reqs")" >> "$OUT/log.txt"
tmux has-session -t "$T" && echo "session alive" >> "$OUT/log.txt"
