#!/bin/bash
# Bytes read/written by the process per second between two epochs of a samples.tsv.
# usage: m22-bytes.sh samples.tsv from_epoch to_epoch
awk -F'\t' -v a="$2" -v b="$3" 'NR>1 && $1>=a && $1<=b {if(!s){s=$1;r0=$7;w0=$8} e=$1;r1=$7;w1=$8}
 END{d=e-s; printf "  %ds  read %.0f B/s  write %.0f B/s\n", d, (r1-r0)/d, (w1-w0)/d}' "$1"
