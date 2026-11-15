#!/bin/bash
# K-*: Kafka screens against the real broker (tier 1, plaintext via port-forward),
# with the -cover binary. Records the screen after each step.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/kafka; rm -rf "$OUT" "$HOME/m22/cov-kafka"; mkdir -p "$OUT" "$HOME/m22/cov-kafka"
export BIN=$HOME/m22/bin/huginn-cover GOCOVERDIR=$HOME/m22/cov-kafka D=1.2 OUT
start rec 200 50; sleep 25
key / c a t a l o g; key Enter; snap 01-filtered-catalog
key M; sleep 5; snap 02-topics
key Enter; sleep 6; snap 03-records-default          # topic-b records
key 0; sleep 3; snap 04-last-per-partition
key 1; sleep 4; snap 05-window-15m
key o; snap 06-order; key o
key / k e y = k 1; key Enter; sleep 2; snap 07-filter-key
key Escape; key / p a r t i t i o n = 2; key Enter; sleep 2; snap 08-filter-partition
key Escape; key / '5' '0'; key Enter; sleep 2; snap 09-filter-text; key Escape
key i; snap 10-isolation; key i
key D; snap 11-raw-toggle; key D
key f; sleep 2; snap 12-follow-off; key f
key space; sleep 3; snap 13-paused; key space
key y; sleep 1; snap 14-copy
key Enter; sleep 2; snap 15-zoom; key J; key K; key Escape
key b; snap 16-range-popup; key Escape
key r; sleep 4; snap 17-reconnect
key '?'; snap 18-help; key Escape
key Escape; sleep 1; key Down; key Enter; sleep 3; snap 19-other-topic
key Escape; key Escape; sleep 1; snap 20-back
key q; sleep 3
ls "$HOME/m22/cov-kafka" | wc -l > "$OUT/covfiles.txt"
