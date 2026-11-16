#!/bin/bash
# C-7: `huginn kafka check|read` with every flag against the real broker, with the -cover binary.
export CLOUDSDK_CONFIG=/mnt/c/Users/PC/AppData/Roaming/gcloud KUBECONFIG=$HOME/m22/kubeconfig SOPS_AGE_KEY_FILE=$HOME/m22/kafka-secrets/age.key
export PATH=$PATH:$HOME/bin
rm -rf $HOME/m22/cov-cli; mkdir -p $HOME/m22/cov-cli; export GOCOVERDIR=$HOME/m22/cov-cli
H=$HOME/m22/bin/huginn-cover; C=$HOME/m22/cfg
run() { echo "\$ kafka $*"; timeout 60 $H --config $C kafka "$@" 2>&1 | head -${N:-3} | cut -c1-200; echo "  exit=${PIPESTATUS[0]}"; }
N=2 run check catalog -e rec
N=3 run read catalog topic-b -e rec --tail 1
N=3 run read catalog topic-b -e rec --since 1h
N=2 run read catalog topic-b -e rec --since 1h --raw
N=2 run read catalog topic-a -e rec --since 2d --no-decode
N=2 run read catalog topic-a -e rec --since 2d
N=2 run read catalog topic-b -e rec --committed --tail 1
N=2 run read catalog topic-b -e rec --from "2020-01-01 00:00" --to "2020-01-01 01:00"
N=2 run read catalog topic-b -e rec --from "now-3h" --to "now-1h"
N=2 run read catalog nosuchtopic -e rec --tail 1
N=2 run read nosuchrepo topic-b -e rec --tail 1
N=2 run check orders -e rec
N=2 run read orders topic-b -e rec --tail 1
echo "# follow: 4 s, a record produced meanwhile"
( sleep 2; kubectl --context gke_huginn-kube-tui_us-east1_huginn-qa -n qa-rec exec deploy/kafka-qa -- sh -c 'echo "kfollow:{\"followed\":true}" | /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server localhost:9092 --topic topic-b --property parse.key=true --property key.separator=: >/dev/null 2>&1' ) &
timeout 8 $H --config $C kafka read catalog topic-b -e rec --tail 1 -f 2>&1 | tail -2 | cut -c1-160; wait
