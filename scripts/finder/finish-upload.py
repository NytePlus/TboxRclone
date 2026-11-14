#!/usr/bin/env python3
"""One-command upload evidence collection; visual review remains mandatory.

Runs content checks, journal hashing, ten-second video extraction and final UI
capture concurrently. Never changes acceptance status or repeats an upload.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
import os
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
CACHE = '/private/tmp/tboxrclone-swift-module-cache'
MOUNT = '/private/tmp/tboxrclone-webdav-501'

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('session', type=Path)
    p.add_argument('evidence', type=Path)
    p.add_argument('--output', type=Path, help='fresh output directory; default evidence/finish')
    p.add_argument('--movie', type=Path, help='alternative movie, e.g. retained recording')
    p.add_argument('--skip-video', action='store_true', help='use final-state evidence only when recording was interrupted or the screen switched')
    p.add_argument('--clear-faults', action='store_true', help='snapshot proxy events then clear injection before content checks')
    a = p.parse_args()
    evidence = a.evidence.resolve()
    out = a.output.resolve() if a.output else evidence/'finish'
    if ROOT/'evidence' not in out.parents or ROOT/'evidence' not in evidence.parents:
        p.error('evidence and output must be inside repository evidence/')
    meta = json.loads((evidence/'run.json').read_text())
    plan = json.loads((a.session/'fault-session.json').read_text())
    env = plan['environment']
    if env['TBOX_LAB_REMOTE'] != 'sjtu:'+meta['remote_root'] or not meta['remote_root'].startswith('codex-api-lab/'):
        p.error('run and session remote roots differ')
    if Path(meta['filename']).name != meta['filename']:
        p.error('run.json requires a basename for the file or copied tree')
    container = 'tboxrclone-webdav-1' if plan.get('transport')=='direct' else 'tboxrclone-webdav-faults-1'
    info = json.loads(subprocess.check_output(['docker','inspect',container],timeout=15))[0]
    actual = dict(x.split('=',1) for x in info['Config']['Env'])
    if not info['State']['Running'] or env['TBOX_LAB_REMOTE'] not in info['Config']['Cmd'] or actual.get('RCLONE_CONFIG_SJTU_STATE_DIR') != env['TBOX_STATE_DIR']:
        p.error('live service differs from session')
    out.mkdir(exist_ok=False)
    started = time.monotonic()
    timings = []
    errors = []
    video_status = {'status':'pending'}
    def run(name, command, stdout=None, allowed=(0,), extra_env=None, timeout=180):
        begin = time.monotonic()
        destination = stdout or out/(name+'.log')
        with destination.open('x') as log, (out/(name+'.stderr.log')).open('x') as errlog:
            result = subprocess.run(command,cwd=ROOT,stdout=log,stderr=errlog,
                env={**os.environ, **(extra_env or {})},timeout=timeout)
        timing = {'phase':name,'elapsed_seconds':time.monotonic()-begin,'exit_code':result.returncode}
        (out/(name+'.timing.json')).write_text(json.dumps(timing,indent=2)+'\n')
        timings.append(timing)
        if result.returncode not in allowed:
            raise RuntimeError(f'{name} failed: exit {result.returncode}; see {name}.stderr.log')
        return result.returncode
    def py(script,*args):
        return ['python3',str(ROOT/'scripts/finder'/script),*map(str,args)]
    def swift(script,*args):
        return ['swift','-module-cache-path',CACHE,str(ROOT/'scripts/finder'/script),*map(str,args)]
    # Capture injection evidence before any clearing or verification traffic.
    if plan.get('transport')=='faults':
        try:
            run('events',py('fault-control.py','events',out/'events.json'))
            if a.clear_faults:
                (out/'clear-rules.json').write_text('[]\n')
                run('clear-faults',py('fault-control.py','rules',out/'clear-rules.json'))
        except Exception as exc:
            errors.append(str(exc))
    elif a.clear_faults:
        errors.append('--clear-faults requires a faults session')
    def content():
        run('content',py('finish-part-evidence.py',evidence,'--content-only','--output',out/'content'),timeout=240)
    def journal():
        state=ROOT/'.state'/Path(env['TBOX_STATE_DIR']).name
        run('journal',py('snapshot-journal.py',state,meta['remote_root']+'/'+meta['filename'],out/'journal.json',*(['--subtree'] if meta.get('tree') else [])))
    def video():
        movie=a.movie.resolve() if a.movie else evidence/'finder/display-1.mov'
        coverage=evidence/'finder/capture-coverage.json'
        interrupted=coverage.exists() and json.loads(coverage.read_text()).get('ended_early',False)
        logs='\n'.join(p.read_text(errors='replace') for p in (evidence/'finder').glob('display-*.log'))
        interrupted=interrupted or 'STREAM_STOPPED' in logs or 'Fatal error:' in logs
        if a.skip_video or interrupted or not movie.is_file():
            video_status.update(status='skipped',reason='interrupted, unavailable, or explicitly skipped; final state review required')
            return
        try:
            run('video',swift('review-recording.swift',movie,out/'review'),timeout=90)
            video_status.update(status='available')
        except Exception as exc:
            video_status.update(status='unavailable',reason=str(exc))
    def native():
        # One native traversal replaces separate dialog/progress/window probes.
        target=str(Path(MOUNT)/meta.get('mount_subtree','')/(meta['filename'] if meta.get('tree') else ''))
        expected=meta['filename']
        if meta.get('tree'):
            entries=sorted(x.name for x in (Path(meta['source_directory'])/meta['filename']).iterdir() if not x.name.startswith('.'))
            expected=entries[0] if entries else None
        inspect_args=[meta['filename'],target,*([expected] if expected else [])]
        status=run('native-inspection',swift('inspect-final-state.swift',*inspect_args),
            out/'native-inspection.json',allowed=(0,2),timeout=35)
        inspection=json.loads((out/'native-inspection.json').read_text())
        dialogs=inspection['dialogs']
        progress=inspection['transfer_windows']
        if status==0 and not dialogs and not progress and not inspection['view_ready']:
            run('reveal',['sh',str(ROOT/'scripts/finder-drag.sh'),'--open',meta['source_directory'],target],
                extra_env={'TBOX_FINDER_SELECT_FILENAME':meta['filename']},timeout=35)
            # Opening a window does not prove its contents have loaded. Require
            # an expected visible entry, without taking intermediate screenshots.
            deadline=time.monotonic()+15
            attempt=0
            while time.monotonic()<deadline:
                attempt+=1
                check=out/f'native-ready-{attempt}.json'
                status=run(f'native-ready-{attempt}',swift('inspect-final-state.swift',*inspect_args),check,allowed=(0,2),timeout=35)
                inspection=json.loads(check.read_text())
                dialogs=inspection['dialogs']
                progress=inspection['transfer_windows']
                if status!=0 or dialogs or progress or inspection['view_ready']:
                    break
                time.sleep(0.5)
        run('final-screenshot',['/usr/sbin/screencapture','-x','-D','1',str(out/'final-task-state.png')],timeout=15)
        run('final-preview',['/usr/bin/sips','-Z','1920',str(out/'final-task-state.png'),'--out',str(out/'final-task-state-preview.png')],timeout=15)
        (out/'native-state.json').write_text(json.dumps({'blocking_dialog':bool(dialogs),'transfer_visible':bool(progress),
            'final_state_candidate':status==0 and not dialogs and not progress and inspection['view_ready'],
            'target_contents_visible':inspection['view_ready'],'inspection_complete':status==0,'visual_review_required':True},indent=2)+'\n')
        if status != 0:
            raise RuntimeError('native inspection incomplete; screenshot retained for review')
        if not dialogs and not progress and not inspection['view_ready']:
            raise RuntimeError('target contents not visible; screenshot is not final acceptance evidence')
    with ThreadPoolExecutor(max_workers=4) as pool:
        jobs={pool.submit(fn):name for name,fn in [('content',content),('journal',journal),('video',video),('native',native)]}
        for job in as_completed(jobs):
            try: job.result()
            except Exception as exc: errors.append(f'{jobs[job]}: {exc}')
    images=[]
    if video_status['status']=='available' and (out/'review.index.json').exists():
        index=json.loads((out/'review.index.json').read_text())
        images += [str(out/s['file']) for s in index['sheets']]
    # Current final task state supersedes an early-ended recording's last frame.
    final_image = out/('final-task-state-preview.png' if (out/'final-task-state-preview.png').exists() else 'final-task-state.png')
    if final_image.exists(): images.append(str(final_image))
    report={'elapsed_seconds':time.monotonic()-started,'phases':timings,'errors':errors,
        'review_images':images,'required_final_image':str(final_image),'original_final_image':str(out/'final-task-state.png'),
        'video':video_status,'acceptance':'REQUIRES_FINAL_TASK_STATE_REVIEW_AND_FACT_VERIFICATION',
        'note':'No automatic PASS. Recording is supplementary; interruption alone does not fail acceptance. Active transfer is not completion.'}
    (out/'summary.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,ensure_ascii=False))
    return 1 if errors else 0

if __name__=='__main__':
    raise SystemExit(main())
