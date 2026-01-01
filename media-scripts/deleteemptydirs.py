#!/usr/bin/env -S uv run
# /// script
# dependencies = ["typer>=0.9.0"]
# ///
"""
Remove empty directories from a specified root directory.

Only scans one level deep (immediate subdirectories).
"""

import sys
from pathlib import Path

import typer

app = typer.Typer(
    help="Remove empty directories from a target directory.",
    add_completion=False,
)


@app.command()
def main(
    root: Path = typer.Option(
        Path("."), "--root", "-r", help="Root directory to scan for empty subdirectories"
    ),
    dry_run: bool = typer.Option(
        False, "--dry-run", "-n", help="Preview changes without deleting any directories"
    ),
) -> None:
    """
    Remove empty directories from a target directory.

    Scans a target directory for empty subdirectories and removes them.
    Only immediate subdirectories (one level deep) are checked.
    """
    target_dir = root.resolve()

    if not target_dir.exists():
        print(f"Error: path does not exist: {target_dir}", file=sys.stderr)
        raise typer.Exit(1)

    if not target_dir.is_dir():
        print(f"Error: path is not a directory: {target_dir}", file=sys.stderr)
        raise typer.Exit(1)

    if dry_run:
        print("DRY RUN - no directories will be deleted\n")

    count = 0

    for entry in target_dir.iterdir():
        if not entry.is_dir():
            continue

        try:
            contents = list(entry.iterdir())
        except PermissionError as e:
            print(f"Error reading {entry}: {e}", file=sys.stderr)
            continue

        if len(contents) == 0:
            if dry_run:
                print(f"Would delete: {entry.name}")
            else:
                try:
                    entry.rmdir()
                    print(f"Deleted: {entry.name}")
                except OSError as e:
                    print(f"Error deleting {entry}: {e}", file=sys.stderr)
                    continue
            count += 1

    action = "would be removed" if dry_run else "removed"
    suffix = "y" if count == 1 else "ies"
    print(f"\n{count} empty director{suffix} {action}")


if __name__ == "__main__":
    app()
