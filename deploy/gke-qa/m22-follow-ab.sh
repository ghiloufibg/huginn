#!/bin/bash
# N-4 follow-then-leave, current binary against the pre-M21 baseline, in parallel.
cd "$(dirname "$0")"
export CLOUDSDK_CONFIG=/mnt/c/Users/PC/AppData/Roaming/gcloud
tmux kill-server 2>/dev/null
pkill -9 -x huginn-plain; pkill -9 -x huginn-baseline
sleep 2
rm -rf "$HOME"/m22/leak-*
for b in plain baseline; do
  BIN=$HOME/m22/bin/huginn-$b T=t-follow-$b OUT=$HOME/m22/leak-follow-$b MODE=follow \
    nohup bash ./m22-drive-leak.sh > "$HOME/m22/leak-follow-$b.log" 2>&1 &
  sleep 6
done
while [ "$(ls "$HOME"/m22/leak-*/stop 2>/dev/null | wc -l)" -lt 2 ]; do sleep 10; done
echo all-finished
