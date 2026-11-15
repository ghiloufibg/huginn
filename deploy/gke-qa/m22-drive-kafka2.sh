#!/bin/bash
# K-*: the 3 000-record, 6-partition topic: tail, window, paging, and the no-consumer-group check.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/kafka2; rm -rf "$OUT"; mkdir -p "$OUT"; export OUT D=1.5
start rec 200 50; sleep 25
key / c a t a l o g; key Enter; key M; sleep 5
key j j j; key Enter; sleep 8; snap 01-range-topic-default
key 0; sleep 5; snap 02-tail
key 6; sleep 12; snap 03-window-1d
key ']'; sleep 5; snap 04-next-page
key '['; sleep 5; snap 05-prev-page
key G; sleep 1; snap 06-bottom; key g; sleep 1; snap 07-top
key o; sleep 3; snap 08-order
key Escape; key Escape; key Escape
