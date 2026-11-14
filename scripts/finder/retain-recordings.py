#!/usr/bin/env python3
"""Append verified clips to a scenario's sole indexed movie, then remove duplicates."""
import argparse
import datetime
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--latest-only', action='store_true', help='replace the existing movie with only the supplied latest evidence; retain old index provenance')
p.add_argument('scenario', type=Path)
p.add_argument('clips', nargs='+', type=Path)
a = p.parse_args()
scenario = a.scenario.resolve()
if scenario.parent != ROOT / 'evidence' or not scenario.name.startswith('ST-'):
    p.error('requires a top-level project evidence scenario')
movie = scenario / 'retained-recording.mov'
index = Path(str(movie) + '.index.json')
if movie.exists() != index.exists():
    p.error('existing movie/index pair is incomplete')
clips = [clip.resolve() for clip in a.clips]
if len(set(clips)) != len(clips):
    p.error('duplicate input clips')
for clip in clips:
    if scenario not in clip.parents or clip == movie or not clip.is_file() or clip.suffix != '.mov':
        p.error('each clip must be a separate movie inside this scenario')
stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
plan_path = scenario / ('recording-plan-' + stamp + '.json')
next_movie = scenario / ('retained-' + stamp + '.mov')
plan = []
if movie.exists() and not a.latest_only:
    segments = json.loads(index.read_text())['segments']
    for segment in segments:
        clip = {'file': str(movie), 'source': segment['source'],
                'offset': segment['start_seconds']}
        if 'duration_seconds' in segment:
            clip['duration'] = segment['duration_seconds']
        elif len(segments) != 1 or segment['start_seconds'] != 0:
            p.error('missing duration in a multi-segment index; original retained')
        # Legacy single-clip indexes describe the entire original movie.
        # AVFoundation resolves its duration and validates it before replacement.
        plan.append(clip)
plan += [{'file': str(clip), 'source': str(clip)} for clip in clips]
plan_path.write_text(json.dumps(plan, indent=2) + '\n')
subprocess.run(['swift', '-module-cache-path', '/private/tmp/tboxrclone-swift-module-cache',
                str(ROOT / 'scripts/finder/consolidate-recordings.swift'),
                str(next_movie), '--plan', str(plan_path)], check=True)
next_index = Path(str(next_movie) + '.index.json')
data = json.loads(next_index.read_text())
if len(data['segments']) != len(plan):
    raise SystemExit('segment count mismatch; original files retained')
# Keep the old index for recovery if interrupted between the two replacements.
if index.exists():
    (scenario / ('recording-index-before-' + stamp + '.json')).write_bytes(index.read_bytes())
os.replace(next_movie, movie)
os.replace(next_index, index)
removed = []
for clip in clips:
    removed.append({'path': str(clip), 'bytes': clip.stat().st_size})
    clip.unlink()
report = {'movie': str(movie), 'segments': len(plan), 'removed': removed,
          'mode': 'latest supplied evidence only' if a.latest_only else 'separate recordings with gaps; not continuous coverage'}
(scenario / ('recording-retention-' + stamp + '.json')).write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps({'movie': str(movie), 'segments': len(plan),
                  'duplicate_bytes_removed': sum(row['bytes'] for row in removed)}))
