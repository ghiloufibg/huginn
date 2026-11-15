#!/bin/bash
# Broad key walk on the real cluster with the -cover binary (docs/plan/M22
# §1, §4): services, logs (windows, filters, display, selection, zoom, trace,
# pickers), usage, env picker, help. Quits with q so that coverage is flushed.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/walk; rm -rf "$OUT" "$HOME/m22/cov"; mkdir -p "$OUT" "$HOME/m22/cov"
export BIN=$HOME/m22/bin/huginn-cover GOCOVERDIR=$HOME/m22/cov D=0.8
start rec 200 50; sleep 25
alive() { tmux has-session -t "$T" 2>/dev/null && tmux capture-pane -p -t "$T" | grep -q 'huginn' ; }
step() { echo "$1 alive=$(alive && echo y || echo NO)" >> "$OUT/steps.txt"; }
# services
key j k g G s s s s; key / d e p l; key Enter; key Escape; key p p; key '?'; key Escape; key F2 F2 F2; key r; sleep 3; step services
# usage
key u; sleep 8; key j Enter j Enter p p s s s s A A; key / u s a g e; key Enter; key Escape; key Space Space L; sleep 4; key Escape; key '?'; key Escape; key Escape; step usage
# logs: open range-emitter
key / r a n g e - e m i t t e r; key Enter; key Enter; sleep 8; snap 20-logs; step logs-open
for k in 1 2 3 4 5 6 7 0 9 t; do key $k; done; sleep 3; key 0; step windows
key T; key Escape; key b; key Escape; key '[' ']'; step pickers
key f; key space; sleep 2; key space; key f; key o o; step follow-order
key l; key Escape; key e; key w; key a; step levels
key / s e q; key Enter; key n N; key x; key X X X X; key '>' '<'; key Escape; key / '!' s e q; key Enter; key Escape Escape; step filters
key c c c c c; key C; key Escape; key C-t C-t C-t; key z z; key R; key W W; key F F; key I I I; key M M; key A A; step display
key Left Right H L; key Tab Tab Tab; key S; key Escape; step pods
key V j j j V; key y; key Escape; key m j m; key Y; key C-s; sleep 2; key Escape; step select
key Enter; sleep 1; key J K; key p p; key Tab Tab BTab; key '='; sleep 1; key Escape; key Escape; step zoom
key v; sleep 4; key Escape; key d; sleep 2; key Escape; key E; sleep 2; key Escape; key P; sleep 2; key P; key B; sleep 2; key Escape; step trace-diag
key '?'; key Escape; key F2 F2 F2; key C-k; step help
key Escape Escape; sleep 2
key C-e; key Down; key Enter; sleep 6; step env-switch
key C-e; key Up; key Enter; sleep 6; key C-p; key d; key Escape; step finder
key q; sleep 3
tmux has-session -t "$T" 2>/dev/null && tmux capture-pane -p -t "$T" | tail -3 >> "$OUT/steps.txt"
ls "$HOME/m22/cov" | wc -l >> "$OUT/steps.txt"
