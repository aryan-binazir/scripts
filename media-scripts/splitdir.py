#!/usr/bin/env -S uv run
# /// script
# dependencies = ["typer>=0.9.0"]
# ///
"""
Split files from a directory into numbered subdirectories (1/, 2/, 3/, etc.).

Uses first-fit-decreasing bin packing for efficient distribution.
Files larger than the limit get their own directory.
"""

import errno
import re
import shutil
import sys
from pathlib import Path

import typer

app = typer.Typer(
    help="Split files into numbered subdirectories by size.",
    add_completion=False,
)

SIZE_UNITS = {
    "B": 1,
    "KB": 1024,
    "MB": 1024**2,
    "GB": 1024**3,
    "TB": 1024**4,
}


def parse_size(size_str: str) -> int:
    """Parse a human-readable size string (e.g., '8GB', '500MB') into bytes."""
    pattern = re.compile(r"^(\d+(?:\.\d+)?)\s*(B|KB|MB|GB|TB)?$", re.IGNORECASE)
    match = pattern.match(size_str.strip())

    if not match:
        raise typer.BadParameter(
            f"invalid size format: '{size_str}' (expected format: number + unit, e.g., 8GB, 500MB, 1TB)"
        )

    num = float(match.group(1))
    unit = (match.group(2) or "B").upper()
    return int(num * SIZE_UNITS[unit])


def format_size(bytes_val: int) -> str:
    """Format bytes as a human-readable string."""
    size = float(bytes_val)
    for unit in ["B", "KB", "MB", "GB", "TB"]:
        if size < 1024:
            return f"{size:.2f} {unit}"
        size /= 1024
    return f"{size:.2f} TB"


def find_max_numbered_dir(source_dir: Path) -> int:
    """Find the maximum existing numbered subdirectory (1/, 2/, etc.)."""
    pattern = re.compile(r"^\d+$")
    max_num = 0

    for entry in source_dir.iterdir():
        if entry.is_dir() and pattern.match(entry.name):
            try:
                num = int(entry.name)
                max_num = max(max_num, num)
            except ValueError:
                continue

    return max_num


def move_file(src: Path, dest: Path) -> None:
    """Move a file, handling cross-filesystem moves."""
    if dest.exists():
        raise FileExistsError(f"destination already exists: {dest}")

    try:
        src.rename(dest)
        return
    except OSError as e:
        if e.errno != errno.EXDEV:
            raise

    shutil.copy2(src, dest)

    if src.stat().st_size != dest.stat().st_size:
        dest.unlink()
        raise RuntimeError(f"copy verification failed: size mismatch for {src}")

    src.unlink()


@app.command()
def main(
    directory: Path = typer.Argument(..., help="Directory containing files to split"),
    split_size: str = typer.Option(
        "8GB", "--split-size", "-s", help="Size limit per subdirectory (e.g., 8GB, 500MB, 1TB)"
    ),
    dry_run: bool = typer.Option(
        False, "--dry-run", "-n", help="Preview changes without moving files"
    ),
) -> None:
    """
    Split files from a directory into numbered subdirectories (1/, 2/, 3/, etc.).

    Uses first-fit-decreasing bin packing for efficient distribution of files.
    Files larger than the size limit are placed in their own directory.
    """
    if not directory.exists():
        print(f"Error: directory does not exist: {directory}", file=sys.stderr)
        raise typer.Exit(1)

    if not directory.is_dir():
        print(f"Error: not a directory: {directory}", file=sys.stderr)
        raise typer.Exit(1)

    max_size = parse_size(split_size)
    if max_size <= 0:
        print(f"Error: split size must be positive, got: {split_size}", file=sys.stderr)
        raise typer.Exit(1)

    if dry_run:
        print("DRY RUN - no files will be moved\n")

    start_num = find_max_numbered_dir(directory)
    if start_num > 0:
        print(f"Found existing numbered directories up to {start_num}/, starting from {start_num + 1}/\n")

    files: list[tuple[str, int]] = []
    for entry in directory.iterdir():
        if entry.is_file():
            files.append((entry.name, entry.stat().st_size))

    if not files:
        print("No files found in directory")
        raise typer.Exit(0)

    files.sort(key=lambda x: x[1], reverse=True)

    batches: list[list[tuple[str, int]]] = []
    batch_sizes: list[int] = []

    for name, size in files:
        if size > max_size:
            print(f"Warning: {name} exceeds {format_size(max_size)} ({format_size(size)}), placing in its own directory")
            batches.append([(name, size)])
            batch_sizes.append(size)
            continue

        placed = False
        for i, batch_size in enumerate(batch_sizes):
            if batch_size + size <= max_size:
                batches[i].append((name, size))
                batch_sizes[i] += size
                placed = True
                break

        if not placed:
            batches.append([(name, size)])
            batch_sizes.append(size)

    print(f"Splitting {len(files)} files into {len(batches)} directories (max {format_size(max_size)} each)\n")

    operations: list[tuple[Path, Path, str]] = []

    for i, batch in enumerate(batches):
        dir_name = str(start_num + i + 1)
        dir_path = directory / dir_name

        if dry_run:
            print(f"Directory {dir_name}: {len(batch)} files ({format_size(batch_sizes[i])})")
            for name, _ in batch:
                print(f"  {name}")

        for name, _ in batch:
            operations.append((directory / name, dir_path / name, dir_name))

    if dry_run:
        print(f"\n{len(operations)} files would be moved")
        raise typer.Exit(0)

    created_dirs: set[str] = set()
    completed = 0

    for src, dest, dir_name in operations:
        dir_path = directory / dir_name

        if str(dir_path) not in created_dirs:
            dir_path.mkdir(parents=True, exist_ok=True)
            created_dirs.add(str(dir_path))
            print(f"\nDirectory {dir_name}:")

        try:
            move_file(src, dest)
        except Exception as e:
            print(f"\nFAILED: {src} -> {dest}", file=sys.stderr)
            print(f"Error: {e}", file=sys.stderr)
            print(f"\nStopping. {completed}/{len(operations)} files moved.", file=sys.stderr)
            print("Re-run to continue with remaining files.", file=sys.stderr)
            raise typer.Exit(1)

        print(f"  Moved: {src} -> {dest}")
        completed += 1

    print(f"\nDone! {completed} files moved into {len(batches)} directories.")


if __name__ == "__main__":
    app()
