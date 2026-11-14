#!/usr/bin/env python3
"""Run a Finder helper with a deadline; terminate only its own process group."""
import argparse
import os
import signal
import subprocess
import sys

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--timeout', type=float, required=True)
parser.add_argument('command', nargs=argparse.REMAINDER)
args = parser.parse_args()
if not args.command or args.timeout <= 0:
    parser.error('positive timeout and command are required')
process = subprocess.Popen(args.command, start_new_session=True)
try:
    code = process.wait(timeout=args.timeout)
except subprocess.TimeoutExpired:
    print(f'helper timed out after {args.timeout:g}s; no acceptance result', file=sys.stderr)
    os.killpg(process.pid, signal.SIGTERM)
    try:
        process.wait(timeout=3)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            # A process in an uninterruptible filesystem syscall may not reap
            # until the mount recovers. Do not turn a deadline into an infinite
            # wait; retain its identity so the caller can inspect it later.
            print(f'helper still unreaped after SIGKILL: pid={process.pid}; '
                  'mount recovery may be required', file=sys.stderr)
    code = 124
raise SystemExit(code if code >= 0 else 128-code)
