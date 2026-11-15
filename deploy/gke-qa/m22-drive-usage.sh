#!/bin/bash
# U-12/U-13: every key of the usage screen against the real cluster.
. "$(dirname "$0")/m22-drive-lib.sh"
start rec 200 50; sleep 25
snap 00-services
key u; sleep 8; snap 01-usage
key j; key enter; snap 02-expand-service          # enter on a service: its pods
key j; key enter; snap 03-expand-pod              # enter on a pod: its containers
key p; snap 04-detail-panel
key p; key s; snap 05-sort-cpu
key s; snap 06-sort-memlimit
key s; snap 07-sort-name
key s; snap 08-sort-memory
key A; sleep 4; snap 09-app-only                  # sidecars out of the sums
key A; sleep 4; snap 10-all-containers
key /; key u s a g e - m; key enter; snap 11-filter
key Escape; snap 12-filter-cleared
key g; key space; snap 13-fold-namespace
key space; snap 14-unfold
key g; key j; key L; sleep 5; snap 15-logs-of-service   # L opens the logs
key Escape; sleep 2; snap 16-back-from-logs
key r; sleep 6; snap 17-reload
key '?'; snap 18-help
key Escape; key Escape; sleep 1; snap 19-back-to-services
