#!/bin/bash
# N-17, C-6, T-4, L-8: redaction on save and copy, clipboard, debug log, and a
# scan of everything the session wrote for secrets.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/secrets; rm -rf "$OUT" "$HOME/m22/saves" "$HOME/.cache/huginn"; mkdir -p "$OUT" "$HOME/m22/saves"; export OUT D=1.5
export BIN=$HOME/m22/bin/huginn-fixed KUBECONFIG=$HOME/m22/kubeconfig
tmux kill-server 2>/dev/null
tmux start-server; tmux set -g set-clipboard on
tmux new-session -d -s "$T" -x 200 -y 50 "BIN=$BIN $HOME/m22/run.sh --log-level debug --repo redact-emitter --since 5m rec; sleep 300"
PTY=$OUT/pty.raw; : > "$PTY"; tmux pipe-pane -t "$T" -o "cat >> $PTY"
sleep 5; for i in $(seq 1 90); do tmux capture-pane -p -t "$T" | grep -q loading || break; sleep 1; done; sleep 4
snap 01-screen
key f; key C-s; sleep 3; snap 02-saved
key g; key y; sleep 2; snap 03-copied; tmux show-buffer > "$OUT/clip-y.txt" 2>&1
key Y; sleep 2; snap 04-copied-raw; tmux show-buffer > "$OUT/clip-Y.txt" 2>&1
key V; key j; key j; key V; key y; sleep 2; tmux show-buffer > "$OUT/clip-range.txt" 2>&1; snap 05-range-copied
key Escape; key Enter; sleep 2; snap 06-zoom; key Escape
key q; sleep 2
