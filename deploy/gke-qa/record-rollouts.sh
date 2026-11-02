#!/bin/sh
# The ledger of the M15.1 QA (docs/plan/M15-gke-qa.md §2.6, §3): the oracle
# for every V case. It records (1) every action done to a fixture, with the
# time it was done and the exact command, and (2) every change of the state
# the controllers report for the fixtures, polled every 2 seconds: the
# Progressing condition's reason and lastUpdateTime, and the replica counts.
#
# The services preview reads exactly these fields, so a displayed date and
# hour is checked against `lastUpdateTime` here, and "nothing moved" against
# the raw value, to the second.
#
# Usage (from WSL, KUBECONFIG pointing at the cluster):
#   deploy/gke-qa/record-rollouts.sh poll   [ledger]   # run in the background
#   deploy/gke-qa/record-rollouts.sh act    <note> -- <command...>
#   deploy/gke-qa/record-rollouts.sh show   [ledger]
# The ledger defaults to $LEDGER, else ./m15-ledger.log (append-only).
set -u
LEDGER=${LEDGER:-./m15-ledger.log}
INTERVAL=${INTERVAL:-2}

# kind/name in namespace: what the preview shows lines for.
WATCH="
qa-rec deployment/deploy-probe
qa-rec deployment/deploy-pair-api
qa-rec deployment/deploy-auto
qa-rec deployment/payment-service
qa-rec statefulset/deploy-sts
qa-rec statefulset/deploy-pair-db
qa-rec daemonset/deploy-ds
"

now() { date -u +%Y-%m-%dT%H:%M:%S.%3NZ; }

# state <ns> <kind/name>: one line of what the controller reports.
state() {
  kubectl -n "$1" get "$2" -o jsonpath='gen={.metadata.generation} observed={.status.observedGeneration} spec={.spec.replicas} updated={.status.updatedReplicas} ready={.status.readyReplicas} avail={.status.availableReplicas} progressing={.status.conditions[?(@.type=="Progressing")].status}/{.status.conditions[?(@.type=="Progressing")].reason}@{.status.conditions[?(@.type=="Progressing")].lastUpdateTime} available={.status.conditions[?(@.type=="Available")].status}@{.status.conditions[?(@.type=="Available")].lastTransitionTime} currentRev={.status.currentRevision} updateRev={.status.updateRevision}' 2>/dev/null
}

cmd=${1:-}
[ $# -gt 0 ] && shift
case "$cmd" in
  poll)
    [ $# -gt 0 ] && LEDGER=$1
    echo "$(now) | ledger | start, polling every ${INTERVAL}s" >>"$LEDGER"
    prev_dir=$(mktemp -d)
    trap 'rm -rf "$prev_dir"; echo "$(now) | ledger | stop" >>"$LEDGER"; exit 0' INT TERM
    while :; do
      echo "$WATCH" | while read -r ns obj; do
        [ -z "$ns" ] && continue
        cur=$(state "$ns" "$obj")
        [ -z "$cur" ] && cur="(absent)"
        f="$prev_dir/$(echo "$ns-$obj" | tr '/' '_')"
        if [ "$(cat "$f" 2>/dev/null)" != "$cur" ]; then
          echo "$(now) | state | $ns $obj | $cur" >>"$LEDGER"
          printf '%s' "$cur" >"$f"
        fi
      done
      sleep "$INTERVAL"
    done
    ;;
  act)
    note=${1:?note}
    shift
    [ "${1:-}" = "--" ] && shift
    [ $# -gt 0 ] || { echo "act: a command is required" >&2; exit 2; }
    echo "$(now) | act   | $note | $*" >>"$LEDGER"
    "$@"
    rc=$?
    echo "$(now) | done  | $note | exit $rc" >>"$LEDGER"
    exit $rc
    ;;
  show)
    [ $# -gt 0 ] && LEDGER=$1
    cat "$LEDGER"
    ;;
  *)
    echo "usage: $0 poll [ledger] | act <note> -- <command...> | show [ledger]" >&2
    exit 2
    ;;
esac
