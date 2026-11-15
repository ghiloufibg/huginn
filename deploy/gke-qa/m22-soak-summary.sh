#!/bin/bash
# N-5: per cycle of the soak, RSS at the end of the 60 s services-idle minute
# (the comparable point), the peak of the cycle, threads and fds at that point.
D=${1:-$HOME/m22/soak}
awk -F'\t' 'FNR==NR { if ($2 ~ /^cycle-/) { n++; start[n]=$1 } ; next }
  FNR>1 { e=$1; r=$2/1024
    for (i=1;i<=n;i++) {
      if (e>=start[i]+55 && !got[i]) { got[i]=1; base[i]=r; th[i]=$4; fd[i]=$5 }
      if (e>=start[i] && (i==n || e<start[i+1]) && r>peak[i]) peak[i]=r
    } }
  END { for (i=1;i<=n;i++) printf "cycle %2d: services-idle rss=%5.1f MiB  thr=%s fds=%s  peak of the cycle=%5.1f MiB\n", i, base[i], th[i], fd[i], peak[i] }' \
  "$D/phases.tsv" "$D/samples.tsv"
