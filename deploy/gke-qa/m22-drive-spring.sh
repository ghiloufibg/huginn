#!/bin/bash
# M22: the Spring trio (payment-service, catalog-indexer, order-orchestrator)
# on the real cluster: services, usage with JVM memory, formatted logs.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/spring; rm -rf "$OUT"; mkdir -p "$OUT"
BIN=${BIN:-$HOME/m22/bin/huginn-plain}; export BIN
start rec 200 50
D=2; sleep 25; snap 01-services
key u; sleep 20; snap 02-usage
key Escape; sleep 1
key / ; key p a y m e n t; key Enter; key Enter; sleep 6; snap 03-payment-logs
key Space; sleep 1; key Enter; sleep 2; snap 04-zoom; key Escape; key Escape; key Escape; sleep 1
key / ; key c a t a l o g; key Enter; key Enter; sleep 6; snap 05-catalog-logs
key Escape; key Escape; sleep 1
key / ; key o r d e r; key Enter; key Enter; sleep 6; snap 06-order-logs
tmux kill-session -t "$T"
