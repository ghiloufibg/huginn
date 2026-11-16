#!/bin/bash
# N-17: patterns that must not appear in what Huginn writes: the Huginn debug
# log, saved files, copied text; and (as a control) what the fixtures log.
# Prints counts per file kind and pattern. A pattern is searched as a fixed string.
LOG=$HOME/.cache/huginn/huginn.log
SAVES=$HOME/m22/saves
PATTERNS=("alice.smith@example.com" "QA-TOKEN" "eyJhbGciOi" "ya29." "Authorization" "BEGIN PRIVATE KEY" "client-certificate-data")
# the QA keystore password is read from the (gitignored) broker config, never written here
KSPW=$(grep "^ssl.keystore.password=" "${M:-/mnt/c/Users/PC/IdeaProjects/huginn/deploy/gke-qa}/kafka-secrets/server.properties" 2>/dev/null | cut -d= -f2-)
[ -n "$KSPW" ] && PATTERNS+=("$KSPW")
count() { grep -a -c -F -- "$2" "$1" 2>/dev/null || true; }
echo "debug log: $LOG ($(wc -c < "$LOG" 2>/dev/null) bytes, $(wc -l < "$LOG" 2>/dev/null) lines)"
for p in "${PATTERNS[@]}"; do
  label=$p; [ -n "${KSPW:-}" ] && [ "$p" = "$KSPW" ] && label="<keystore password>" # never print it
  printf '  %-28s debug-log=%s  saves=%s\n' "$label" "$(count "$LOG" "$p")" "$(cat "$SAVES"/* 2>/dev/null | grep -a -c -F -- "$p")"
done
echo "control: the fixture really logs the secret (kubectl logs):"
KUBECONFIG=$HOME/m22/kubeconfig kubectl --context gke_huginn-kube-tui_us-east1_huginn-qa -n qa-rec logs deploy/redact-emitter --tail=3 2>&1 | cut -c1-170
