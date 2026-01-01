#!/usr/bin/env -S uv run
# /// script
# dependencies = ["typer>=0.9.0"]
# ///
"""
Move all files from one or more source directories into a single target directory.

Recursively scans subdirectories and flattens the structure.
Handles filename collisions by appending a numeric suffix (e.g., photo_1.jpg).
Supports cross-filesystem moves (copy + delete with verification).
"""

import errno
import hashlib
import shutil
import sys
from pathlib import Path

import typer

app = typer.Typer(
    help="Consolidate files from multiple directories into one.",
    add_completion=False,
)

claimed_names: set[str] = set()


def check_path_overlap(target_dir: Path, source_dirs: list[Path]) -> None:
    """Detect unsafe path relationships between target and source directories."""
    abs_target = target_dir.resolve()

    for src in source_dirs:
        abs_src = src.resolve()

        if abs_target == abs_src:
            raise typer.BadParameter(
                f"target directory '{target_dir}' is the same as source directory '{src}'"
            )

        if is_subpath(abs_target, abs_src):
            raise typer.BadParameter(
                f"target directory '{target_dir}' is inside source directory '{src}'"
            )

        if is_subpath(abs_src, abs_target):
            raise typer.BadParameter(
                f"source directory '{src}' is inside target directory '{target_dir}'"
            )


def is_subpath(child: Path, parent: Path) -> bool:
    """Return True if child is a subdirectory of parent."""
    try:
        child.relative_to(parent)
        return True
    except ValueError:
        return False


def get_all_files(directory: Path) -> list[Path]:
    """Recursively get all regular files in a directory."""
    files = []
    try:
        for entry in directory.iterdir():
            if entry.is_dir():
                files.extend(get_all_files(entry))
            elif entry.is_file():
                files.append(entry)
    except PermissionError as e:
        print(f"Warning: {e}", file=sys.stderr)
    return files


def get_unique_name(target_dir: Path, filename: str) -> Path:
    """Get a unique filename in target_dir, handling collisions."""
    stem = Path(filename).stem
    suffix = Path(filename).suffix
    candidate = target_dir / filename
    counter = 1

    while candidate.exists() or str(candidate) in claimed_names:
        candidate = target_dir / f"{stem}_{counter}{suffix}"
        counter += 1

    claimed_names.add(str(candidate))
    return candidate


def compute_sha256(filepath: Path) -> str:
    """Compute SHA256 checksum of a file."""
    sha256 = hashlib.sha256()
    with open(filepath, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            sha256.update(chunk)
    return sha256.hexdigest()


def move_file(src: Path, dest: Path, verify: bool) -> None:
    """Move a file, handling cross-filesystem moves."""
    if dest.exists():
        raise FileExistsError(f"destination file already exists (no-clobber): {dest}")

    src_checksum = None
    if verify:
        src_checksum = compute_sha256(src)

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

    if verify:
        dest_checksum = compute_sha256(dest)
        if src_checksum != dest_checksum:
            dest.unlink()
            raise RuntimeError(
                f"checksum verification failed for {src}: source={src_checksum} dest={dest_checksum}"
            )
        print(f"  [verified] SHA256: {src_checksum}")

    src.unlink()


@app.command()
def main(
    target_dir: Path = typer.Argument(..., help="Target directory to move files into"),
    source_dirs: list[Path] = typer.Argument(..., help="Source directories to consolidate"),
    dry_run: bool = typer.Option(
        False, "--dry-run", "-n", help="Preview changes without moving any files"
    ),
    verify: bool = typer.Option(
        False, "--verify", help="Verify SHA256 checksums after copy (slower but safer)"
    ),
) -> None:
    """
    Consolidate files from multiple source directories into a single target directory.

    Recursively scans all source directories and moves files into the target,
    flattening the directory structure. Handles filename collisions by appending
    a numeric suffix (e.g., photo.jpg becomes photo_1.jpg).
    """
    if not source_dirs:
        raise typer.BadParameter("at least one source directory is required")

    check_path_overlap(target_dir, source_dirs)

    if target_dir.exists():
        for entry in target_dir.iterdir():
            claimed_names.add(str(target_dir / entry.name))

    if dry_run:
        print("DRY RUN - no files will be moved\n")
    else:
        target_dir.mkdir(parents=True, exist_ok=True)

    operations: list[tuple[Path, Path, bool]] = []

    for source_dir in source_dirs:
        if not source_dir.exists():
            print(f"Skipping {source_dir}: not found or not accessible")
            continue

        files = get_all_files(source_dir)
        for filepath in files:
            simple_dest = target_dir / filepath.name
            final_dest = get_unique_name(target_dir, filepath.name)
            was_renamed = final_dest != simple_dest
            operations.append((filepath, final_dest, was_renamed))

    if not operations:
        print("No files found to move.")
        raise typer.Exit(0)

    renamed_count = sum(1 for _, _, renamed in operations if renamed)

    if dry_run:
        for src, dest, renamed in operations:
            suffix = " (renamed)" if renamed else ""
            print(f"Would move: {src} -> {dest}{suffix}")
        plural = "s" if len(operations) != 1 else ""
        print(f"\n{len(operations)} file{plural} would be moved ({renamed_count} renamed to avoid duplicates)")
        raise typer.Exit(0)

    if verify:
        print("Checksum verification enabled (SHA256)")

    completed = 0
    for src, dest, renamed in operations:
        try:
            move_file(src, dest, verify)
        except Exception as e:
            print(f"\nFAILED: {src} -> {dest}", file=sys.stderr)
            print(f"Error: {e}", file=sys.stderr)
            print(f"\nStopping. {completed}/{len(operations)} files moved successfully.", file=sys.stderr)
            print("Re-run the script to continue with remaining files.", file=sys.stderr)
            raise typer.Exit(1)

        suffix = " (renamed)" if renamed else ""
        print(f"Moved: {src} -> {dest}{suffix}")
        completed += 1

    plural = "s" if completed != 1 else ""
    print(f"\n{completed} file{plural} moved ({renamed_count} renamed to avoid duplicates)")


if __name__ == "__main__":
    app()
