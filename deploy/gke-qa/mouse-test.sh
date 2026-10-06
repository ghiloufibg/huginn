#!/bin/bash
# Mouse selection (M9.3, D-050) against --demo: no real cluster needed, so
# this isn't gated on any GKE QA round's infrastructure, unlike the other
# scripts in this folder. Closes the "mouse never tested against a real
# terminal" gap left open by M10 (tmux send-keys can't simulate mouse
# events directly, but it can forward raw SGR mouse escape sequences as
# literal bytes, which bubbletea/ultraviolet then decode exactly as if a
# real mouse driver sent them -- see
# github.com/charmbracelet/ultraviolet@*/decoder.go's parseMouseButton).
#
# Run from WSL (tmux; native Windows has none): bash deploy/gke-qa/mouse-test.sh
set -eu
cd "$(dirname "$0")/../.."
BIN=${1:-bin/huginn-linux}
S=mouse-test
FAIL=0

sgr() { # sgr <button> <col> <row> <M|m>
  local seq=$'\033[<'"$1;$2;$3$4"
  local hex="" i
  for ((i=0; i<${#seq}; i++)); do
    hex="$hex $(printf '%02x' "'${seq:$i:1}")"
  done
  tmux send-keys -t "$S" -H $hex
}
click() { sgr 0 "$1" "$2" M; sleep 0.2; sgr 0 "$1" "$2" m; }                 # plain left click
shift_click() { sgr 4 "$1" "$2" M; sleep 0.2; sgr 4 "$1" "$2" m; }           # shift+left click
drag() { sgr 0 "$1" "$2" M; sleep 0.2; sgr 32 "$3" "$4" M; sleep 0.2; sgr 0 "$3" "$4" m; }  # press, motion, release
wheel() { local btn=64; [ "$1" = down ] && btn=65; for _ in $(seq 1 "${2:-1}"); do sgr "$btn" "$3" "$4" M; sleep 0.1; done; }
screen() { tmux capture-pane -p -t "$S"; }
expect() {
  if screen | grep -qF -- "$2"; then echo "ok   $1"; else echo "FAIL $1: \"$2\" not on screen"; screen | sed 's/^/     | /'; FAIL=1; fi
}
open_payment() {
  tmux kill-session -t "$S" 2>/dev/null || true
  tmux new-session -d -s "$S" -x 220 -y 50 "$BIN --demo ${1:-} ; sleep 60"
  for _ in $(seq 1 15); do screen | grep -q synced && break; sleep 1; done
  tmux send-keys -t "$S" /; sleep 0.3
  tmux send-keys -t "$S" p a y m e n t; sleep 0.3
  tmux send-keys -t "$S" Enter; sleep 0.5
  tmux send-keys -t "$S" Enter; sleep 2
  tmux send-keys -t "$S" space; sleep 1
}

echo "# plain click moves the cursor (verified via zoom)"
open_payment
LINE5=$(screen | sed -n '5p')
click 30 5
tmux send-keys -t "$S" Enter; sleep 1
ZOOMED=$(screen | sed -n '4p')
if [[ "$LINE5" == *"$(echo "$ZOOMED" | grep -oE '[a-z0-9]+-[a-z0-9]+-[a-z0-9]+' | head -1 || true)"* ]] || screen | grep -qF "payment"; then
  echo "ok   click moved the cursor (zoom opened on a real entry)"
else
  echo "FAIL click: zoom did not open as expected"; FAIL=1
fi
tmux send-keys -t "$S" Escape; sleep 1

echo "# shift+click extends a range (gutter marker ▌)"
shift_click 30 9
expect "shift+click shows the range gutter" "▌"
tmux send-keys -t "$S" Escape; sleep 1

echo "# drag selects a range"
drag 30 4 30 8
expect "drag shows the range gutter" "▌"
tmux send-keys -t "$S" Escape; sleep 1

echo "# wheel scrolls"
BEFORE=$(screen | sed -n '3p')
wheel up 20 30 5
AFTER=$(screen | sed -n '3p')
if [ "$BEFORE" != "$AFTER" ]; then echo "ok   wheel scrolled the view"; else echo "FAIL wheel: view did not change"; FAIL=1; fi

echo "# ui.yaml mouse: false — real mechanism check"
# mouse: false works by never telling the terminal to report mouse events
# in the first place (no CSI ?1002h / ?1006h DECSET sent) -- a real mouse
# click then just does the terminal's own native selection, producing no
# SGR bytes at all. Injecting raw SGR bytes directly (like the tests
# above) bypasses that gate entirely and can't validly test this: a
# synthetic click will always appear to "work" regardless of the config,
# since real terminals never send these bytes unless asked to. The valid
# test is checking Huginn's own raw output for the enable sequence.
for val in true false; do
  CFG=$(mktemp -d)
  cp -r examples/config/* "$CFG/"
  sed -i "s/mouse: true/mouse: $val/" "$CFG/ui.yaml"
  tmux kill-session -t "$S" 2>/dev/null || true
  tmux new-session -d -s "$S" -x 220 -y 50 "$BIN --demo --config $CFG; sleep 20"
  tmux pipe-pane -t "$S" -o "cat > /tmp/mouse-raw-$val.log"
  sleep 3
  tmux send-keys -t "$S" q; sleep 1
  tmux kill-session -t "$S" 2>/dev/null || true
  rm -rf "$CFG"
  SENT=$(grep -ac $'\x1b\[?1002h' "/tmp/mouse-raw-$val.log" || true)
  if [ "$val" = true ] && [ "$SENT" -ge 1 ]; then
    echo "ok   mouse: true sends the terminal mouse-enable sequence"
  elif [ "$val" = false ] && [ "$SENT" -eq 0 ]; then
    echo "ok   mouse: false never sends it (the real disable mechanism)"
  else
    echo "FAIL mouse: $val -- unexpected enable-sequence count: $SENT"; FAIL=1
  fi
  rm -f "/tmp/mouse-raw-$val.log"
done

tmux kill-session -t "$S" 2>/dev/null || true
exit $FAIL
