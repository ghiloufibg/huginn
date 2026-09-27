#!/bin/sh
# Starts Huginn's M4 lab: a local Kubernetes cluster in Docker (kind) with
# dummy workloads covering every state Huginn shows. See README.md.
# Usage: deploy/lab/up.sh [minikube]   (default: kind)
set -eu
cd "$(dirname "$0")"
IMAGES="alpine:3.20 nginx:1.27-alpine"
for img in $IMAGES; do docker image inspect "$img" >/dev/null 2>&1 || docker pull "$img"; done

if [ "${1:-kind}" = minikube ]; then
  minikube start --driver=docker
  for img in $IMAGES; do minikube image load "$img"; done
  CONTEXT=minikube
else
  kind get clusters 2>/dev/null | grep -qx huginn || kind create cluster --config kind.yaml --image "${KIND_IMAGE:-kindest/node:v1.33.12}"
  # "kind load docker-image" fails with Docker >= 29 on multi-platform
  # images (content digest not found): import one platform through ctr.
  for img in $IMAGES; do
    docker save --platform linux/amd64 "$img" | docker exec -i huginn-control-plane ctr -n k8s.io images import --platform linux/amd64 -
  done
  CONTEXT=kind-huginn
fi

# A new namespace gets its default ServiceAccount a moment after creation;
# until then a bare pod (debug-shell) is refused: retry.
for i in 1 2 3 4 5 6 7 8 9 10; do
  if kubectl --context "$CONTEXT" apply -f workloads.yaml -f rbac.yaml; then break; fi
  [ "$i" = 10 ] && exit 1
  sleep 3
done
kubectl --context "$CONTEXT" -n app-rec create configmap loggen --from-file=loggen.sh --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f -
kubectl --context "$CONTEXT" -n app-rec rollout restart deploy/payment-service deploy/payment-worker >/dev/null
# The read-only identity of rbac.yaml, as the context huginn-restricted.
CLUSTER=$(kubectl config view -o jsonpath="{.contexts[?(@.name==\"$CONTEXT\")].context.cluster}")
kubectl config set-credentials huginn-reader --token="$(kubectl --context "$CONTEXT" -n app-rec create token huginn-reader --duration=24h)" >/dev/null
kubectl config set-context huginn-restricted --cluster="$CLUSTER" --user=huginn-reader >/dev/null
echo
echo "Lab ready (context $CONTEXT). Try: huginn --config deploy/lab/config rec"
