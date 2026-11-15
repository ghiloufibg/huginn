#!/bin/bash
# After `f` (follow off) on a loaded logs screen: how long does the screen show 0 lines?
export CLOUDSDK_CONFIG=/mnt/c/Users/PC/AppData/Roaming/gcloud KUBECONFIG=$HOME/m22/kubeconfig
for b in fixed baseline; do
  for run in 1 2 3; do
    tmux kill-server 2>/dev/null
    tmux new-session -d -s s -x 200 -y 50 "BIN=$HOME/m22/bin/huginn-$b $HOME/m22/run.sh --repo prd-api --since 15m prd; sleep 120"
    for i in $(seq 1 60); do tmux capture-pane -p -t s | grep -q loading || break; sleep 1; done; sleep 4
    t0=$(date +%s%N); tmux send-keys -t s f
    blank=0; first=""
    for i in $(seq 1 100); do
      scr=$(tmux capture-pane -p -t s)
      ms=$(( ($(date +%s%N)-t0)/1000000 ))
      if echo "$scr" | grep -q 'prd-api line'; then [ -z "$first" ] && first=$ms; else blank=$ms; fi
      [ -n "$first" ] && [ "$ms" -gt $((first+300)) ] && break
      sleep 0.05
    done
    echo "$b run$run: last blank poll at ${blank} ms, lines visible again at ${first:-never} ms"
  done
done
tmux kill-server 2>/dev/null
