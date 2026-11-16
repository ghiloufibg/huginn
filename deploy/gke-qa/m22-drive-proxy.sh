#!/bin/bash
# M22 O-4: per-verb API count, Metrics API outage and network cut, through
# tools/m22-apiproxy.go (no audit logs needed). Run in WSL with
# CLOUDSDK_CONFIG set. Needs ~/m22/bin/m22-apiproxy and ~/m22/bin/huginn-plain.
. "$(dirname "$0")/m22-drive-lib.sh"
CTX=gke_huginn-kube-tui_us-east1_huginn-qa
P=$HOME/m22/api; rm -rf "$P"; mkdir -p "$P"; OUT=$HOME/m22/proxy; rm -rf "$OUT"; mkdir -p "$OUT"
export CLOUDSDK_CONFIG=${CLOUDSDK_CONFIG:-/mnt/c/Users/PC/AppData/Roaming/gcloud}
REAL=$HOME/m22/kubeconfig
SERVER=$(KUBECONFIG=$REAL kubectl config view --raw --minify --context $CTX -o jsonpath='{.clusters[0].cluster.server}')
KUBECONFIG=$REAL kubectl config view --raw --minify --context $CTX -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d > "$P/api-ca.pem"
tmux kill-session -t pxy 2>/dev/null || true
tmux new-session -d -s pxy "$HOME/m22/bin/m22-apiproxy -upstream $SERVER -upstream-ca $P/api-ca.pem -dir $P 2>&1 | tee $P/proxy.log; sleep 3600"
until [ -s "$P/proxy-ca.pem" ]; do sleep 1; done
# the proxied kubeconfig: same user (exec plugin), server and CA replaced
KUBECONFIG=$REAL kubectl config view --raw --minify --context $CTX > "$P/kubeconfig"
sed -i "s#server: .*#server: https://127.0.0.1:8443#; /certificate-authority-data/d" "$P/kubeconfig"
sed -i "0,/server: https/s##certificate-authority-data: $(base64 -w0 "$P/proxy-ca.pem")\n    server: https#" "$P/kubeconfig"
export KUBECONFIG=$P/kubeconfig
kubectl --context $CTX get ns >/dev/null && echo "proxy path works" || { echo "proxy path FAILED"; exit 1; }
: > "$P/requests.tsv"
BIN=${BIN:-$HOME/m22/bin/huginn-plain}
start() { tmux kill-session -t "$T" 2>/dev/null || true
  tmux new-session -d -s "$T" -x "${2:-200}" -y "${3:-50}" "KUBECONFIG=$P/kubeconfig BIN=$BIN $HOME/m22/run.sh $1; sleep 900"; }
ms() { date +%s%3N; }
mark() { echo -e "$1\t$(ms)" >> "$OUT/marks.tsv"; }

start rec 200 50
sleep 40; snap 01-services-idle; mark services-start
sleep 60; mark services-end
D=2; key u; sleep 10; mark usage-start; snap 02-usage
sleep 150; mark usage-end                      # ten refreshes at 15 s
key Escape; key / ; key n o i s y; key Enter; key Enter; sleep 5; snap 03-logs
mark logs-start; sleep 60; mark logs-end; snap 03b-logs-after
key Escape; key Escape; sleep 2; key u; sleep 20; snap 04-usage-again

# B: the Metrics API goes away (503), then comes back
touch "$P/metrics503"; mark metrics503-on; sleep 50; snap 05-metrics503-50s
sleep 70; snap 06-metrics503-120s
rm "$P/metrics503"; mark metrics503-off; sleep 20; snap 07-metrics-back-20s
key r; sleep 3; snap 08-metrics-back-r; sleep 40; snap 09-metrics-back-60s

# C: a network cut for 60 s on the usage screen, then restored
touch "$P/cut"; mark cut-on; sleep 30; snap 10-cut-30s
sleep 40; snap 11-cut-70s
rm "$P/cut"; mark cut-off; sleep 20; snap 12-restored-20s
key r; sleep 3; snap 13-restored-r; sleep 30; snap 14-restored-50s
# C2: the same cut on the logs screen (follow), then back
key Escape; sleep 1; key Enter; sleep 1; key Enter; sleep 6; snap 15-logs-before-cut
touch "$P/cut"; mark cut2-on; sleep 40; snap 16-logs-cut
rm "$P/cut"; mark cut2-off; sleep 30; snap 17-logs-restored
key r; sleep 5; snap 18-logs-r
mark end
cp "$P/requests.tsv" "$OUT/"; tmux kill-session -t "$T"; tmux kill-session -t pxy
# per-phase counts: verb and shaped path, status
phase() { awk -F'\t' -v a="$1" -v b="$2" -v f="$OUT/marks.tsv" 'BEGIN{while((getline l<f)>0){split(l,x,"\t"); m[x[1]]=x[2]}} $1>=m[a] && $1<m[b]{c[$3" "$4" "$5]++} END{for(k in c) print c[k]"\t"k}' "$OUT/requests.tsv" | sort -rn; }
{ for p in "services-start services-end" "usage-start usage-end" "logs-start logs-end"; do echo "== $p"; phase $p; done; } > "$OUT/counts.txt"
cat "$OUT/counts.txt"
