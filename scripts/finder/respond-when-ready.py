#!/usr/bin/env python3
"""Bounded wait for one exact native dialog action; never handles passwords."""
import argparse
import json
from pathlib import Path
import subprocess
import time

ROOT=Path(__file__).resolve().parents[2]
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('marker')
p.add_argument('button')
p.add_argument('output',type=Path)
p.add_argument('--after-drag-log',type=Path)
p.add_argument('--seconds',type=int,default=60)
a=p.parse_args()
if a.output.exists() or not 1<=a.seconds<=120: p.error('fresh output and1..120seconds required')
start=time.monotonic();attempts=[];success=False
while time.monotonic()-start<a.seconds:
    if a.after_drag_log and (not a.after_drag_log.exists() or 'drag_delivered' not in a.after_drag_log.read_text()):
        time.sleep(0.5);continue
    r=subprocess.run(['swift','-module-cache-path','/private/tmp/tboxrclone-swift-module-cache',str(ROOT/'scripts/finder/respond.swift'),a.marker,a.button],capture_output=True,text=True,timeout=15)
    attempts.append({'exit_code':r.returncode,'stdout':r.stdout,'stderr':r.stderr})
    if r.returncode==0: success=True;break
    if r.returncode!=2: break
    time.sleep(0.5)
report={'marker':a.marker,'button':a.button,'action_performed':success,'seconds':time.monotonic()-start,'attempts':attempts}
a.output.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'action_performed':success,'attempts':len(attempts)}))
raise SystemExit(0 if success else 1)
