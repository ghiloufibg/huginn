#!/bin/bash
# M22 sampler (docs/plan/M22-gke-full-qa.md §5): once a second, for the
# process matching $1, one TSV line: epoch rss_kb hwm_kb threads fds
# cpu_ticks rchar wchar tcp_established pty_bytes. Stops when the process ends
# or the file $OUT/stop exists.
PAT=$1; OUT=$2; PTY=${3:-}
echo -e "epoch\trss_kb\thwm_kb\tthreads\tfds\tcpu_ticks\trchar\twchar\testab\tpty_bytes" > "$OUT/samples.tsv"
while [ ! -e "$OUT/stop" ]; do
  # $PAT is a tmux session name: the huginn process is the child of the pane's
  # shell (matching by command line picked the wrong process when several ran).
  PANE=$(tmux display -p -t "$PAT" '#{pane_pid}' 2>/dev/null)
  PID=$(pgrep -P "$PANE" 2>/dev/null | head -1); [ -z "$PID" ] && { sleep 1; continue; }
  rss=$(awk '/VmRSS/{print $2}' /proc/$PID/status 2>/dev/null)
  hwm=$(awk '/VmHWM/{print $2}' /proc/$PID/status 2>/dev/null)
  thr=$(awk '/Threads/{print $2}' /proc/$PID/status 2>/dev/null)
  fds=$(ls /proc/$PID/fd 2>/dev/null | wc -l)
  cpu=$(awk '{print $14+$15}' /proc/$PID/stat 2>/dev/null)
  rch=$(awk '/^rchar/{print $2}' /proc/$PID/io 2>/dev/null)
  wch=$(awk '/^wchar/{print $2}' /proc/$PID/io 2>/dev/null)
  est=$(ss -tnp 2>/dev/null | grep -c "pid=$PID,.*" )
  pty=0; [ -n "$PTY" ] && [ -e "$PTY" ] && pty=$(stat -c %s "$PTY")
  echo -e "$(date +%s)\t$rss\t$hwm\t$thr\t$fds\t$cpu\t$rch\t$wch\t$est\t$pty" >> "$OUT/samples.tsv"
  sleep 1
done
