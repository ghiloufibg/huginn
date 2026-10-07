#!/bin/sh
# Runs govulncheck and fails only on a vulnerability not already triaged
# and explicitly accepted below -- never silently, and never for a new,
# unreviewed finding. See D-070 for why each accepted id is accepted.
#
# govulncheck has no built-in per-id ignore list (checked: v1.8.0's -h
# output has none), so this wrapper does the filtering itself: it still
# runs the real scan, still fails CI on anything not on the list, and the
# list is the only thing a reviewer needs to re-check when a new finding
# appears.
set -eu

# id: why it is accepted, and when to revisit.
ACCEPTED="
GO-2026-5046 # hamba/avro CPU exhaustion decoding Avro; no fixed version exists (checked 2026-10-07). Huginn's avrojson.go only calls hamba/avro's schema-metadata/parsing accessors (Type, Items, Fields, Freeze, ParseWithCache) in its real call paths, never hamba's own value-decoding codec internals where this lives; the registry Decode call is also now panic-recovered (D-061's own reasoning, extended). Revisit when hamba/avro ships a fix.
GO-2026-5047 # hamba/avro integer overflow/narrowing panic decoding Avro; same reasoning and mitigation as GO-2026-5046.
GO-2026-5048 # hamba/avro unbounded map allocation decoding Avro; same reasoning and mitigation as GO-2026-5046.
"

OUT=$(mktemp)
trap 'rm -f "$OUT"' EXIT
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 -json ./... >"$OUT" || true

found=$(jq -r 'select(.finding.trace[0].function) | .finding.osv' "$OUT" | tr -d '\r' | sort -u)
unaccepted=""
for id in $found; do
	if ! printf '%s' "$ACCEPTED" | grep -q "^$id "; then
		unaccepted="$unaccepted $id"
	fi
done

if [ -n "$unaccepted" ]; then
	echo "govulncheck: new, unreviewed finding(s):$unaccepted" >&2
	echo "triage with govulncheck -json ./... | jq, then add to ACCEPTED above (with a D-0xx decision) or fix" >&2
	exit 1
fi

if [ -n "$found" ]; then
	echo "govulncheck: only already-accepted findings (see ACCEPTED above, D-070):"
	for id in $found; do echo "  $id"; done
fi
echo "ok"
