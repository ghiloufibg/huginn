#!/bin/bash
# K-*: Avro and JSON Schema records in the TUI, `D` raw/decoded, zoom, with the -cover binary.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/avro; rm -rf "$OUT" "$HOME/m22/cov-avro"; mkdir -p "$OUT" "$HOME/m22/cov-avro"
export BIN=$HOME/m22/bin/huginn-cover GOCOVERDIR=$HOME/m22/cov-avro KUBECONFIG=$HOME/m22/kubeconfig D=1.5 OUT
start rec 200 50; sleep 25
key / c a t a l o g; key Enter; key M; sleep 5; snap 01-topics
key Enter; sleep 6; key 7; sleep 6; snap 02-topic-a-decoded
key D; sleep 3; snap 03-topic-a-raw
key D; sleep 3
key Enter; sleep 2; snap 04-zoom-avro; key J; sleep 1; snap 05-zoom-next; key K; key Escape
key j j; sleep 1; key Enter; sleep 2; snap 06-zoom-bogus; key Escape
key Escape; sleep 2; key j; key Enter; sleep 6; key 7; sleep 6; snap 07-next-topic
key Escape; key j; key Enter; sleep 6; key 7; sleep 6; snap 08-third-topic
key q; sleep 3
