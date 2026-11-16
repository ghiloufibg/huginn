#!/bin/bash
# M22 scale fixture: 20 namespaces x 3 services (one single-replica deployment each).
# usage: m22-scale-fixture.sh apply|delete   (needs CLOUDSDK_CONFIG, KUBECONFIG set)
set -eu
K="kubectl --context gke_huginn-kube-tui_us-east1_huginn-qa"
for i in $(seq -w 1 20); do
  ns=qa-s$i
  if [ "$1" = delete ]; then $K delete ns "$ns" --wait=false --ignore-not-found; continue; fi
  $K create ns "$ns" --dry-run=client -o yaml | $K apply -f - >/dev/null
  for s in web api worker; do
    cat <<Y | $K apply -f - >/dev/null
apiVersion: apps/v1
kind: Deployment
metadata: {name: $s, namespace: $ns, labels: {app.kubernetes.io/part-of: $s-$i}}
spec:
  replicas: 1
  selector: {matchLabels: {app: $s}}
  template:
    metadata: {labels: {app: $s, app.kubernetes.io/part-of: $s-$i}}
    spec:
      containers:
        - name: main
          image: busybox:1.36
          command: [sh, -c, "while true; do echo \$(date) tick; sleep 5; done"]
          resources:
            requests: {cpu: 50m, memory: 64Mi}
            limits: {cpu: 100m, memory: 128Mi}
Y
  done
done
