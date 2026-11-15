#!/bin/bash
# L-1, L-8, L-10: what Huginn shows and saves, against `kubectl logs` as the oracle.
. "$(dirname "$0")/m22-drive-lib.sh"
OUT=$HOME/m22/oracle; rm -rf "$OUT" "$HOME/m22/saves"; mkdir -p "$OUT" "$HOME/m22/saves"; export OUT D=1.2
export BIN=$HOME/m22/bin/huginn-fixed KUBECONFIG=$HOME/m22/kubeconfig
A="kubectl --request-timeout=120s --context gke_huginn-kube-tui_us-east1_huginn-qa"
loaded() { for i in $(seq 1 90); do tmux capture-pane -p -t "$T" | grep -q "loading" || break; sleep 1; done; sleep 3; }
open_logs() { # open_logs <env> <flags...>
  local env=$1; shift
  tmux kill-session -t "$T" 2>/dev/null
  tmux new-session -d -s "$T" -x 200 -y 50 "BIN=$BIN $HOME/m22/run.sh $* $env; sleep 600"
}
newest() { ls -t "$HOME/m22/saves"/* 2>/dev/null | head -1; }

# A. prd-api: one line every 5 s, "prd-api line N"; window 15m; follow off; save what is displayed
open_logs prd --repo prd-api --since 15m; sleep 5; loaded; key f; snap A-screen
key C-s; sleep 3; snap A-after-save; FA=$(newest); echo "A file: $FA" >> "$OUT/result.txt"
$A -n qa-prd logs deploy/prd-api --since=20m > "$OUT/A-kubectl.txt" 2>&1
grep -o 'prd-api line [0-9]*' "$FA" | awk '{print $3}' | sort -n > "$OUT/A-huginn.nums"
grep -o 'prd-api line [0-9]*' "$OUT/A-kubectl.txt" | awk '{print $3}' | sort -n > "$OUT/A-kubectl.nums"
echo "A huginn lines=$(wc -l < "$OUT/A-huginn.nums") first=$(head -1 "$OUT/A-huginn.nums") last=$(tail -1 "$OUT/A-huginn.nums") dups=$(uniq -d "$OUT/A-huginn.nums" | wc -l)" >> "$OUT/result.txt"
echo "A missing-inside-range=$(awk 'NR>1 && $1!=p+1{m+=$1-p-1} {p=$1} END{print m+0}' "$OUT/A-huginn.nums")  extra(not in kubectl)=$(comm -23 "$OUT/A-huginn.nums" "$OUT/A-kubectl.nums" | wc -l)" >> "$OUT/result.txt"
echo "A kubectl(20m) first=$(head -1 "$OUT/A-kubectl.nums") last=$(tail -1 "$OUT/A-kubectl.nums") total=$(wc -l < "$OUT/A-kubectl.nums")" >> "$OUT/result.txt"

# B. range-emitter, 5 minutes: per-pod seq must be contiguous, no duplicate, subset of kubectl

FA_COPY=$HOME/m22/oracle/A-saved.txt; cp "$FA" "$FA_COPY" 2>/dev/null; rm -f "$HOME/m22/saves"/*; open_logs rec --repo range-emitter --since 5m; sleep 5; loaded; key f; key C-s; sleep 8; snap B-after-save; FB=$(newest)
echo "B file: $FB size=$(stat -c %s "$FB" 2>/dev/null)" >> "$OUT/result.txt"
for p in 0 1; do
  grep -o "pod=range-emitter-$p seq=[0-9]*" "$FB" | sed 's/.*seq=//' | sort -n > "$OUT/B-h$p.nums"
  $A -n qa-rec logs range-emitter-$p --since=8m 2>/dev/null | grep -o "seq=[0-9]*" | sed 's/seq=//' | sort -n > "$OUT/B-k$p.nums"
  echo "B pod $p huginn lines=$(wc -l < "$OUT/B-h$p.nums") first=$(head -1 "$OUT/B-h$p.nums") last=$(tail -1 "$OUT/B-h$p.nums") dups=$(uniq -d "$OUT/B-h$p.nums" | wc -l) missing=$(awk 'NR>1 && $1!=p+1{m+=$1-p-1} {p=$1} END{print m+0}' "$OUT/B-h$p.nums") extra=$(comm -23 "$OUT/B-h$p.nums" "$OUT/B-k$p.nums" | wc -l)  kubectl(8m) lines=$(wc -l < "$OUT/B-k$p.nums")" >> "$OUT/result.txt"
done

# C. trace view: lines sharing the trace id of the line under the cursor, on all pods
open_logs rec --repo trace-emitter --since 15m; sleep 5; loaded; key f; key g; snap C-before
key v; sleep 4; snap C-trace-view
N=$(grep -o "of trace [0-9]*" "$OUT/C-before.txt" | head -1 | awk '{print $4}'); TID=$(printf "trace-%04d" "${N:-0}"); echo "C top line trace=$TID" >> "$OUT/result.txt"
n=0; for pod in $($A -n qa-rec get pods -l app=trace-emitter -o name); do
  c=$($A -n qa-rec logs $pod --since=20m | grep -c "\"traceId\":\"$TID\""); n=$((n+c)); echo "C kubectl $pod lines with $TID: $c" >> "$OUT/result.txt"; done
echo "C kubectl total=$n" >> "$OUT/result.txt"
key Escape; snap C-back
