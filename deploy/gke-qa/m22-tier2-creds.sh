#!/bin/bash
# New SCRAM password for qa-user (alphanumeric, never printed), set on the broker and written
# to a local sops+age file (same age key as the M12 one). Only ~/m22/kafka-secrets is written.
set -eu
export CLOUDSDK_CONFIG=/mnt/c/Users/PC/AppData/Roaming/gcloud KUBECONFIG=$HOME/m22/kubeconfig
export PATH=$PATH:$HOME/bin:/mnt/c/Users/PC/AppData/Local/Google/Cloud\ SDK/google-cloud-sdk/bin
export SOPS_AGE_KEY_FILE=$HOME/m22/kafka-secrets/age.key
K="kubectl --context gke_huginn-kube-tui_us-east1_huginn-qa -n qa-rec"
D=$HOME/m22/kafka-secrets
old=$(sops -d --input-type dotenv --output-type dotenv $D/kafka-tier2.env.sops)
TSPW=$(echo "$old" | grep '^KAFKA_TRUSTSTORE_PASSWORD=' | cut -d= -f2-)
PW=$(head -c 40 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24)
$K exec deploy/kafka-qa -- /opt/kafka/bin/kafka-configs.sh --bootstrap-server localhost:9092 --alter --add-config "SCRAM-SHA-512=[iterations=4096,password=$PW]" --entity-type users --entity-name qa-user 2>&1 | tail -1
PUB=$(age-keygen -y $D/age.key)
umask 077
printf 'KAFKA_BOOTSTRAP=localhost:9094\nKAFKA_USER=qa-user\nKAFKA_PASSWORD=%s\nKAFKA_TRUSTSTORE_PASSWORD=%s\n' "$PW" "$TSPW" > /tmp/tier2.env
sops --encrypt --age "$PUB" --input-type dotenv --output-type dotenv /tmp/tier2.env > $D/kafka-tier2.env.sops
rm -f /tmp/tier2.env
echo "re-encrypted: $(sops -d --input-type dotenv --output-type dotenv $D/kafka-tier2.env.sops | cut -d= -f1 | tr '\n' ' ')"
