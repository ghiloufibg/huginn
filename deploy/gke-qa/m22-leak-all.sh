#!/bin/bash
# N-4 isolated, four runs in parallel: {logs,env} cycling x {current,baseline}
# binary, then a long settle. Kills leftovers by exact process name (a
# `pkill -f` pattern matched the shell that ran it and killed it).
cd "$(dirname "$0")"
export CLOUDSDK_CONFIG=/mnt/c/Users/PC/AppData/Roaming/gcloud
tmux kill-server 2>/dev/null
pkill -9 -x huginn-plain; pkill -9 -x huginn-baseline
sleep 2
rm -rf "$HOME"/m22/leak-*
for mode in logs env; do for b in plain baseline; do
  BIN=$HOME/m22/bin/huginn-$b T=t-$mode-$b OUT=$HOME/m22/leak-$mode-$b MODE=$mode \
    nohup bash ./m22-drive-leak.sh > "$HOME/m22/leak-$mode-$b.log" 2>&1 &
  sleep 6
done; done
sleep 60
for d in "$HOME"/m22/leak-*/; do echo "$(basename "$d"): rss_kb/threads/fds = $(tail -1 "$d/samples.tsv" | cut -f2,4,5)"; done
while [ "$(ls "$HOME"/m22/leak-*/stop 2>/dev/null | wc -l)" -lt 4 ]; do sleep 10; done
echo all-finished
