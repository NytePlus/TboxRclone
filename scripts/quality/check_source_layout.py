"""Fail when first-party source files grow beyond the repository size limit."""

from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parents[2]
LIMIT = 500
SOURCE_SUFFIXES = {".c", ".go", ".h", ".js", ".py", ".sh", ".swift", ".ts", ".tsx"}
EXCLUDED_PREFIXES = {
    Path(".git"),
    Path(".state"),
    Path("evidence"),
    Path("third_party/rclone"),
}


def excluded(relative: Path) -> bool:
    return any(relative == prefix or prefix in relative.parents for prefix in EXCLUDED_PREFIXES)


oversized: list[tuple[Path, int]] = []
for source in ROOT.rglob("*"):
    if not source.is_file() or source.suffix not in SOURCE_SUFFIXES:
        continue
    relative = source.relative_to(ROOT)
    if excluded(relative):
        continue
    with source.open("rb") as handle:
        lines = sum(1 for _ in handle)
    if lines > LIMIT:
        oversized.append((relative, lines))

if oversized:
    for relative, lines in sorted(oversized):
        print(f"{relative}: {lines} lines exceeds {LIMIT}", file=sys.stderr)
    raise SystemExit(1)

print(f"source_layout_ok max_lines={LIMIT} excluded=third_party/rclone,generated-state,evidence")
