#!/bin/bash
# Reissue the QA Kafka TLS material (the M12 CA and broker certificate expired on 2026-10-17):
# new CA, broker certificate (same SANs), PKCS12 keystore for the broker, PKCS12 truststore for
# Huginn. Passwords: the keystore ones are in server.properties (QA throwaway), the truststore one is
# KAFKA_TRUSTSTORE_PASSWORD of the sops file; none is printed. Writes only under ~/m22/tls and ~/m22/kafka-secrets.
set -eu
export CLOUDSDK_CONFIG=/mnt/c/Users/PC/AppData/Roaming/gcloud KUBECONFIG=$HOME/m22/kubeconfig
export PATH=$PATH:$HOME/bin:/mnt/c/Users/PC/AppData/Local/Google/Cloud\ SDK/google-cloud-sdk/bin
export SOPS_AGE_KEY_FILE=$HOME/m22/kafka-secrets/age.key
M=/mnt/c/Users/PC/IdeaProjects/huginn/deploy/gke-qa
K="kubectl --context gke_huginn-kube-tui_us-east1_huginn-qa -n qa-rec"
T=$HOME/m22/tls; rm -rf "$T"; mkdir -p "$T"; cd "$T"
KSPW=$(grep '^ssl.keystore.password=' $M/kafka-secrets/server.properties | cut -d= -f2-)
TSPW=$(sops -d --input-type dotenv --output-type dotenv $HOME/m22/kafka-secrets/kafka-tier2.env.sops | grep '^KAFKA_TRUSTSTORE_PASSWORD=' | cut -d= -f2-)
openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.crt -days 30 -subj "/CN=huginn-qa-kafka-ca-2" 2>/dev/null
openssl req -newkey rsa:2048 -nodes -keyout broker.key -out broker.csr -subj "/CN=kafka-qa" 2>/dev/null
printf 'subjectAltName=DNS:kafka-qa,DNS:kafka-qa.qa-rec.svc.cluster.local,DNS:localhost\n' > ext.cnf
openssl x509 -req -in broker.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out broker.crt -days 30 -extfile ext.cnf 2>/dev/null
openssl pkcs12 -export -inkey broker.key -in broker.crt -certfile ca.crt -name kafka-qa -passout "pass:$KSPW" -out broker.keystore.p12
rm -f truststore.p12; keytool -importcert -alias qa-ca -file ca.crt -keystore truststore.p12 -storetype PKCS12 -storepass "$TSPW" -noprompt >/dev/null 2>&1
echo "new broker certificate valid until: $(openssl x509 -in broker.crt -noout -enddate | cut -d= -f2)"
cp truststore.p12 $HOME/m22/kafka-secrets/client.truststore.p12
# the secret keeps its qa-user-password key; only the keystore changes
$K create secret generic kafka-qa-tls --from-file=broker.keystore.p12=broker.keystore.p12 \
  --from-literal=qa-user-password="$(head -c 18 /dev/urandom | base64 | tr -d '/+=')" --dry-run=client -o yaml | $K apply -f - 2>&1 | tail -1
$K rollout restart deploy/kafka-qa 2>&1 | tail -1
for i in $(seq 1 40); do s=$($K get pods -l app=kafka-qa --no-headers | grep -v Terminating | awk '{print $2" "$3}' | head -1); [ "$s" = "1/1 Running" ] && break; sleep 10; done
echo "broker: $s"
