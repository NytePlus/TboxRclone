#!/usr/bin/env python3
"""Native cross-device mv with a held cloud confirmation and retained reference."""
import argparse
import hashlib
import json
import subprocess
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MOUNT = Path('/private/tmp/tboxrclone-webdav-501')
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('session', type=Path)
p.add_argument('evidence', type=Path)
a = p.parse_args()
plan = json.loads((a.session/'fault-session.json').read_text())
env = plan['environment']
remote = env['TBOX_LAB_REMOTE'].removeprefix('sjtu:')
if plan.get('transport') != 'faults' or not remote.startswith('codex-api-lab/ops-'):
    p.error('requires isolated ops-* fault session')
live=json.loads(subprocess.check_output(['docker','inspect','tboxrclone-webdav-faults-1'],timeout=15))[0]
actual=dict(x.split('=',1) for x in live['Config']['Env'])
if not live['State']['Running'] or 'sjtu:'+remote not in live['Config']['Cmd'] or actual.get('RCLONE_CONFIG_SJTU_STATE_DIR')!=env['TBOX_STATE_DIR']:
    p.error('live service differs from session')
subprocess.run(['python3',str(ROOT/'scripts/mount-webdav-macos.py'),'check'],check=True,timeout=30)
out=a.evidence.resolve()
if ROOT/'evidence' not in out.parents: p.error('evidence must be inside repository')
name='cross-disk.bin'
if (MOUNT/name).exists(): p.error('destination already exists')
out.mkdir(parents=True,exist_ok=False)
for directory in ['input','source']: (out/directory).mkdir()
payload=bytes(range(256))*(9*1024*1024//256)
source=out/'input'/name
reference=out/'source'/name
source.write_bytes(payload)
reference.write_bytes(payload)
digest=hashlib.sha256(payload).hexdigest()
del payload
if source.stat().st_dev==MOUNT.stat().st_dev: p.error('fixture is not across filesystems')
(out/'run.json').write_text(json.dumps({'filename':name,'source_directory':str(out/'source'),'remote_root':remote},indent=2)+'\n')
rules=[{'id':f'part-observe-{i}','host':host,'method':'PUT','path_prefix':'/','part_number':2,'min_content_length':1048576,'nth':1,'action':'observe'} for i,host in enumerate(env['TBOX_FAULT_HOSTS'].split(',')) if host.startswith('s3pan')]
rules.append({'id':'hold-cross-confirm','host':'pan.sjtu.edu.cn:443','method':'POST','path_prefix':'/api/v1/file/','action_key':'confirm','nth':1,'action':'hold_response','after_any':[r['id'] for r in rules]})
(out/'rules.json').write_text(json.dumps(rules,indent=2)+'\n')
def control(*args):
    subprocess.run(['python3',str(ROOT/'scripts/finder/fault-control.py'),*map(str,args)],check=True,timeout=30)
def events():
    command=['docker','compose','-f',str(ROOT/'docker-compose.yml'),'-f',str(ROOT/'docker-compose.doh.yml'),'exec','-T','fault-proxy','curl','--fail','--silent','--max-time','5','http://127.0.0.1:8788/events']
    return json.loads(subprocess.check_output(command,timeout=15))
state=ROOT/'.state'/Path(env['TBOX_STATE_DIR']).name
def records():
    return [r for f in state.glob('*.json') if (r:=json.loads(f.read_text())).get('path')==remote+'/'+name and r.get('size')==9437184]
report={'source_sha256':digest,'bytes':9437184,'source_device':source.stat().st_dev,'mount_device':MOUNT.stat().st_dev}
def save(): (out/'result.json').write_text(json.dumps(report,indent=2)+'\n')
control('rules',out/'rules.json')
held=None
with (out/'mv.log').open('x') as log:
    process=subprocess.Popen(['mv',str(source),str(MOUNT/name)],stdout=log,stderr=log)
    try:
        deadline=time.monotonic()+120
        while time.monotonic()<deadline:
            current=events()
            hits=[e for e in current if e.get('rule')=='hold-cross-confirm' and e['phase']=='held']
            if hits:
                held=hits[-1]['request']
                break
            if process.poll() is not None: raise RuntimeError('mv ended before confirmation hold')
            time.sleep(0.5)
        if held is None: raise RuntimeError('confirm was not held')
        rows=records()
        report['held_record_states']=[r['state'] for r in rows]
        report['source_intact_while_confirm_held']=source.exists() and hashlib.sha256(source.read_bytes()).hexdigest()==digest
        report['mv_running_while_confirm_held']=process.poll() is None
        save()
        if len(rows)!=1 or rows[0]['state']!='CommitSent' or not report['source_intact_while_confirm_held'] or not report['mv_running_while_confirm_held']:
            raise RuntimeError('source retention before confirm acknowledgement failed')
        (out/'events-held.json').write_text(json.dumps(current,indent=2)+'\n')
        control('release',held)
        held=None
        observations=[]
        deadline=time.monotonic()+90
        while time.monotonic()<deadline:
            # Check source first: the following durable state must be committed
            # whenever source disappearance is observed. This is sampled evidence.
            present=source.exists()
            rows=records()
            states=[r['state'] for r in rows]
            entry={'at':time.time(),'source_present':present,'states':states}
            if not observations or (present,states)!=(observations[-1]['source_present'],observations[-1]['states']): observations.append(entry)
            if not present and states!=['Committed']: raise RuntimeError('source absent before observed durable commit')
            if process.poll() is not None: break
            time.sleep(0.02)
        report['mv_exit_code']=process.wait(timeout=5)
        report['observations']=observations
        report['source_removed']=not source.exists()
        report['mounted_sha256']=hashlib.sha256((MOUNT/name).read_bytes()).hexdigest()
        report['committed']=len(records())==1 and records()[0]['state']=='Committed'
        report['pass']=report['mv_exit_code']==0 and report['source_removed'] and report['committed'] and report['mounted_sha256']==digest
        report['acceptance']='REQUIRES_FINAL_STATE_AND_INDEPENDENT_CLOUD_VERIFICATION'
        save()
    except Exception as exc:
        report['error']=str(exc);report['pass']=False;save()
        raise
    finally:
        if held is not None: control('release',held)
        if process.poll() is None:
            process.terminate();process.wait(timeout=10)
        control('events',out/'events-final.json')
        (out/'clear.json').write_text('[]\n');control('rules',out/'clear.json')
print(json.dumps(report))
raise SystemExit(0 if report['pass'] else 1)
