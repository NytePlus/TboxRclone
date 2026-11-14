#!/usr/bin/env python3
"""Replay pinned patches in a temporary checkout of touched files and compare bytes."""
from pathlib import Path
import json
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
UPSTREAM = ROOT / 'third_party/rclone'
PATCHES = ROOT / 'third_party/patches/rclone'
base = (PATCHES / 'base').read_text().strip()
patches = sorted(PATCHES.glob('*.patch'))
paths = sorted({line.split(' b/', 1)[1] for patch in patches
                for line in patch.read_text().splitlines() if line.startswith('diff --git a/')})
with tempfile.TemporaryDirectory(prefix='tbox-patch-replay-') as directory:
    work = Path(directory)
    subprocess.run(['git', 'init', '-q', str(work)], check=True)
    for path in paths:
        result = subprocess.run(['git', 'show', base + ':' + path], cwd=UPSTREAM, capture_output=True)
        if result.returncode:
            # New files must be explicitly absent from the pinned tree.
            exists = subprocess.check_output(['git', 'ls-tree', base, '--', path], cwd=UPSTREAM)
            if exists:
                raise RuntimeError('Unable to read pinned file: ' + path)
            continue
        target = work / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(result.stdout)
    for patch in patches:
        subprocess.run(['git', 'apply', '--check', str(patch)], cwd=work, check=True)
        subprocess.run(['git', 'apply', str(patch)], cwd=work, check=True)
    for path in paths:
        if (work / path).read_bytes() != (UPSTREAM / path).read_bytes():
            raise RuntimeError('Patched checkout differs: ' + path)
print(json.dumps({'base': base, 'patches': len(patches), 'files': len(paths), 'byte_identical': True}))
