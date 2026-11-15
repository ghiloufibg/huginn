#!/bin/bash
# U-6/R-4: the usage screen under huginn-reader. qa-rec has the metrics
# grant, qa-prd does not, qa-restricted has no Role: each must be marked on
# its own and the others must still work.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/out-restricted; mkdir -p "$OUT"
start restricted 200 50; sleep 25
snap 00-services
key u; sleep 10; snap 01-usage
key G; snap 02-bottom
key ctrl+e; snap 03-env-picker
key Escape; key Escape; snap 04-after-esc
