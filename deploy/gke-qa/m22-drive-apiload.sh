#!/bin/bash
# N-7: API load of the usage screen. Three timed phases; the UTC times go to
# $OUT/phases.txt and are used to query apiserver_request_total afterwards.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/apiload; rm -rf "$OUT"; mkdir -p "$OUT"
mark() { echo "$1 $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$OUT/phases.txt"; }
start rec 200 50
bash "$(dirname "$0")/m22-sample.sh" "$T" "$OUT" &
SAMP=$!
sleep 60
mark services-only-start; sleep 150
mark usage-open; key u; sleep 300
mark usage-left; key Escape; sleep 180
mark end; touch "$OUT/stop"; wait $SAMP
