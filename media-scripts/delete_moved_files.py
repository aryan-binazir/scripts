#!/usr/bin/env -S uv run
# /// script
# dependencies = ["typer>=0.9.0"]
# ///
"""
Delete files that have been moved to a different directory, usually after a copy function has failed.
"""

from pathlib import Path
import hashlib
import sys
import typer

app = typer.Typer(
    help="Delete files that have been moved to a different directory, "
    "usually after a copy function has failed.",
    add_completion=False,
)


def md5_hash(path):
    hash_md5 = hashlib.md5()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(4096), b""):
            hash_md5.update(chunk)
    return hash_md5.hexdigest()


@app.command()
def main(
    from_dir: Path = typer.Option(
        ...,
        "--from-dir",
        help="Dir copying files from where files already copied will be deleted from",
    ),
    to_dir: Path = typer.Option(
        ...,
        "--to-dir",
        help="Dir where files have been copied to (destination)",
    ),
    dry_run: bool = typer.Option(
        False,
        "--dry-run",
        "-n",
        help="Preview changes without deleting any files",
    ),
) -> None:
    if not from_dir.exists() or not to_dir.exists():
        print("Error: path does not exist", file=sys.stderr)
        raise typer.Exit(1)

    if not from_dir.is_dir() or not to_dir.is_dir():
        print("Error: one of directories passed in is not a directory", file=sys.stderr)
        raise typer.Exit(1)

    if dry_run:
        print("DRY RUN - no files will be deleted\n")

    # Algorithm (flat directories only):
    # 1. Build dict from to_dir: {filename: (path, size)}
    to_dict = {}
    for f in to_dir.iterdir():
        if f.is_file():
            to_dict[f.name] = (f, f.stat().st_size)

    # 2. Iterate through from_dir, for each file:
    for from_file in from_dir.iterdir():
        if not from_file.is_file():
            continue

        filename = from_file.name

        # a. Check if filename exists in to_dir dict
        if filename not in to_dict:
            continue

        to_file, to_size = to_dict[filename]
        from_size = from_file.stat().st_size

        # b. Quick size check - if sizes differ, skip (definitely different files)
        if from_size != to_size:
            continue  # Different sizes = different files, skip

        # c. If size matches, compute MD5 hash and compare
        from_hash = md5_hash(from_file)
        to_hash = md5_hash(to_file)

        # d. If hash matches, delete file from from_dir (or log if dry_run)
        if from_hash == to_hash:
            if dry_run:
                print(f"Would delete: {from_file}")
            else:
                from_file.unlink()
                print(f"Deleted: {from_file}")


if __name__ == "__main__":
    app()
