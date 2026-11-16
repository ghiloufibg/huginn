#!/bin/bash
# Tier 2 Kafka (SASL_SSL, SCRAM-SHA-512, a private CA, credentials in a sops+age file):
# make the broker's qa-user password the one of the encrypted file (nothing is printed), then
# run the profile of the "orders" repo through a port-forward of the SASL listener.
export CLOUDSDK_CONFIG=/mnt/c/Users/PC/AppData/Roaming/gcloud KUBECONFIG=$HOME/m22/kubeconfig
export PATH=$PATH:$HOME/bin:/mnt/c/Users/PC/AppData/Local/Google/Cloud\ SDK/google-cloud-sdk/bin
M=/mnt/c/Users/PC/IdeaProjects/huginn/deploy/gke-qa
K="kubectl --context gke_huginn-kube-tui_us-east1_huginn-qa -n qa-rec"
mkdir -p ~/m22/kafka-secrets
cp $M/kafka-secrets/{kafka-tier2.env.sops,client.truststore.p12,age.key} ~/m22/kafka-secrets/ && chmod 600 ~/m22/kafka-secrets/age.key
cp $M/config-m15/kafka/10-orders.yaml ~/m22/cfg/kafka/10-orders.yaml
export SOPS_AGE_KEY_FILE=$HOME/m22/kafka-secrets/age.key
vars=$(sops -d --input-type dotenv --output-type dotenv ~/m22/kafka-secrets/kafka-tier2.env.sops 2>&1 | grep -c '=')
echo "decrypted variables: $vars ($(sops -d --input-type dotenv --output-type dotenv ~/m22/kafka-secrets/kafka-tier2.env.sops | cut -d= -f1 | tr '\n' ' '))"
PW=$(sops -d --input-type dotenv --output-type dotenv ~/m22/kafka-secrets/kafka-tier2.env.sops | grep '^KAFKA_PASSWORD=' | cut -d= -f2-)
$K exec deploy/kafka-qa -- /opt/kafka/bin/kafka-configs.sh --bootstrap-server localhost:9092 --alter --add-config "SCRAM-SHA-512=[password=$PW]" --entity-type users --entity-name qa-user 2>&1 | tail -1
tmux -L pf kill-session -t sasl 2>/dev/null
tmux -L pf new-session -d -s sasl "while true; do kubectl --context gke_huginn-kube-tui_us-east1_huginn-qa -n qa-rec port-forward svc/kafka-qa 9094:9094; sleep 1; done"
sleep 6
H=$HOME/m22/bin/huginn-fixed; C=$HOME/m22/cfg
echo "--- check orders (SASL_SSL)"; $H --config $C kafka check orders -e rec 2>&1 | head -12 | cut -c1-200
echo "--- read topic-a"; $H --config $C kafka read orders topic-a -e rec --tail 2 2>&1 | head -4 | cut -c1-200
echo "--- wrong password (must fail clearly)"
cp $HOME/m22/cfg/kafka/10-orders.yaml /tmp/orders.bak
