#!/bin/bash
# D on topic-a: decoded <-> bytes, and the zoom of a decoded record and of the bogus-id record.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/avro2; rm -rf "$OUT" "$HOME/m22/cov-avro2"; mkdir -p "$OUT" "$HOME/m22/cov-avro2"
export BIN=$HOME/m22/bin/huginn-cover GOCOVERDIR=$HOME/m22/cov-avro2 KUBECONFIG=$HOME/m22/kubeconfig D=1.5 OUT
start rec 200 50; sleep 25
key / c a t a l o g; key Enter; key M; sleep 5
key j; key Enter; sleep 6; key 7; sleep 6; snap 01-decoded
key D; sleep 4; snap 02-bytes
key D; sleep 4; snap 03-decoded-again
key g; key Enter; sleep 2; snap 04-zoom-decoded; key Escape
key j j j; key Enter; sleep 2; snap 05-zoom-bogus; key Escape
key i; sleep 3; snap 06-isolation
key q; sleep 3
