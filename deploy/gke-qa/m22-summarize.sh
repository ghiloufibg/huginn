#!/bin/bash
# Summarise leak-* runs: rss/threads/fds at each phase, and the peak.
for d in "$HOME"/m22/leak-*/; do
  n=$(basename "$d"); echo "== $n samples=$(($(wc -l < "$d/samples.tsv")-1))"
  awk -F'\t' 'NR==FNR{ph[$2]=$1; next} FNR>1{e[FNR]=$1; r[FNR]=$2; t[FNR]=$4; f[FNR]=$5; N=FNR} END{
    split("base peak-follow after-cycles settled",ks," ");
    for(i=1;i<=4;i++){k=ks[i]; best=2; bd=1e18; for(j=2;j<=N;j++){d=e[j]-ph[k]+0; if(d<0)d=-d; if(d<bd){bd=d;best=j}}
      printf "  %-13s rss=%4d MiB thr=%s fds=%s\n", k, r[best]/1024, t[best], f[best]}
    pk=0; for(j=2;j<=N;j++) if(r[j]>pk)pk=r[j]; printf "  peak          rss=%4d MiB\n", pk/1024}' "$d/phases.tsv" "$d/samples.tsv"
done
