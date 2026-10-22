#!/bin/bash
cd "$(dirname "$0")"
for rep in 1 2 3 4 5; do
  for arm in A M E; do
    case $((rep % 3)) in 1) order="A M E";; 2) order="M E A";; 0) order="E A M";; esac
  done
  for arm in $order; do
    python3 drive.py $arm-chain $arm chain $rep 8 || exit 1
    COMPACT_RATIO=3 python3 drive.py $arm-compact $arm compact $rep 8 || exit 1
    INTERRUPT_AFTER=7 python3 drive.py $arm-interrupt $arm interrupt $rep || exit 1
    python3 drive.py $arm-unverif $arm unverifiable $rep || exit 1
  done
done
echo finished > "${LM_RUNS:-runs}/batch-done"
