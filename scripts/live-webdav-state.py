#!/usr/bin/env python3
"""Snapshot and restore the live Compose WebDAV configuration without secrets."""

import json
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parent.parent
COMPOSE = [
    "docker", "compose", "-f", str(ROOT / "docker-compose.yml"),
    "-f", str(ROOT / "docker-compose.doh.yml"),
]


def option(command: list[str], name: str, default: str = "") -> str:
    try:
        return command[command.index(name) + 1]
    except (ValueError, IndexError):
        return default


def inspect(container: str) -> dict:
    raw = subprocess.check_output(["docker", "inspect", container], text=True)
    return json.loads(raw)[0]


def snapshot(output: Path) -> None:
    if output.exists():
        raise SystemExit(f"refusing to overwrite snapshot: {output}")
    container = subprocess.run(
        COMPOSE + ["--profile", "live", "ps", "-q", "webdav"],
        check=True, text=True, stdout=subprocess.PIPE,
    ).stdout.strip()
    state: dict[str, object] = {"present": bool(container)}
    if container:
        service = inspect(container)
        command = service["Config"]["Cmd"]
        environment = dict(item.split("=", 1) for item in service["Config"]["Env"] if "=" in item)
        try:
            remote = command[command.index("webdav") + 1]
        except (ValueError, IndexError):
            raise SystemExit("running service command does not contain serve webdav remote")
        state["compose_environment"] = {
            "TBOX_LAB_REMOTE": remote,
            "TBOX_RCLONE_CONFIG": option(command, "--config", "/secrets/rclone.conf"),
            "TBOX_WEBDAV_BASE_URL": option(command, "--baseurl"),
            "TBOX_DIR_CACHE_TIME": option(command, "--dir-cache-time", "5m"),
            "TBOX_STATE_DIR": environment.get("RCLONE_CONFIG_SJTU_STATE_DIR", "/state/sjtu"),
            "TBOX_OWNERSHIP_DIR": environment.get("RCLONE_SJTU_OWNERSHIP_DIR", "/state/owners"),
            "TBOX_MAX_UPLOAD": environment.get("RCLONE_SJTU_MAX_UPLOAD", "2G"),
            "TBOX_MAX_SPOOL": environment.get("RCLONE_SJTU_MAX_SPOOL", "4G"),
            "TBOX_LOG_LEVEL": environment.get("RCLONE_LOG_LEVEL", "NOTICE"),
            "TBOX_LAB_OVERWRITE": environment.get("RCLONE_SJTU_LAB_OVERWRITE", "false"),
            "TBOX_LAB_DELETE": environment.get("RCLONE_SJTU_LAB_DELETE", "false"),
            "TBOX_LAB_MOVE": environment.get("RCLONE_SJTU_LAB_MOVE", "false"),
        }
    output.write_text(json.dumps(state, indent=2, sort_keys=True) + "\n")


def restore(source: Path) -> None:
    state = json.loads(source.read_text())
    if not state.get("present"):
        return
    environment = os.environ.copy()
    values = state.get("compose_environment")
    if not isinstance(values, dict) or not values.get("TBOX_LAB_REMOTE"):
        raise SystemExit("invalid live-service snapshot")
    environment.update({str(key): str(value) for key, value in values.items()})
    subprocess.run(
        COMPOSE + ["--profile", "live", "up", "-d", "--force-recreate", "webdav"],
        check=True, env=environment,
    )


if len(sys.argv) != 3 or sys.argv[1] not in {"snapshot", "restore"}:
    raise SystemExit("usage: live-webdav-state.py snapshot|restore STATE.json")
path = Path(sys.argv[2]).resolve()
if sys.argv[1] == "snapshot":
    snapshot(path)
else:
    restore(path)
