#!/usr/bin/env -S uv run
# /// script
# dependencies = ["typer>=0.9.0"]
# ///
"""
Force Google Photos re-upload by adding EXIF comment to media files.
Requires: exiftool (pacman -S perl-image-exiftool)
"""

from pathlib import Path
import subprocess
import shutil
import typer

app = typer.Typer(add_completion=False)

SUPPORTED_EXTENSIONS = {".jpg", ".jpeg", ".heic", ".mov", ".mp4", ".png"}


def check_exiftool() -> bool:
    return shutil.which("exiftool") is not None


def add_comment(path: Path, comment: str) -> bool:
    """Add comment to media file using exiftool."""
    try:
        # -Description works across JPEG, HEIC, MOV, MP4, PNG
        # -UserComment as fallback for EXIF-only readers
        result = subprocess.run(
            [
                "exiftool",
                "-overwrite_original",
                f"-Description={comment}",
                f"-UserComment={comment}",
                str(path),
            ],
            capture_output=True,
            text=True,
        )
        # Check for actual failure (not just warnings)
        if result.returncode != 0:
            print(f"  Error: {result.stderr.strip()}")
            return False
        if "0 image files updated" in result.stdout:
            print(f"  Warning: no tags written")
            return False
        return True
    except Exception as e:
        print(f"  Error: {e}")
        return False


@app.command()
def main(
    directory: Path = typer.Argument(..., help="Directory containing media files"),
    limit: int = typer.Option(
        0, "--limit", "-n", help="Max files to process (0 = unlimited)"
    ),
    comment: str = typer.Option("synced", "--comment", "-c", help="Comment to add"),
    dry_run: bool = typer.Option(False, "--dry-run", help="Preview without modifying"),
    exclude: str = typer.Option(
        None,
        "--exclude",
        "-x",
        help="Comma-separated extensions to exclude (e.g. mov,mp4)",
    ),
) -> None:
    if not check_exiftool():
        print("Error: exiftool not found. Install with: pacman -S perl-image-exiftool")
        raise typer.Exit(1)

    if not directory.exists() or not directory.is_dir():
        print(f"Error: {directory} is not a valid directory")
        raise typer.Exit(1)

    # Warn about MTP risk
    if "mtp:" in str(directory) or "gvfs" in str(directory):
        print("⚠️  Warning: MTP detected. Writes over MTP can fail/corrupt files.")
        print("   Consider copying files locally first.\n")

    # Build exclusion set
    excluded = set()
    if exclude:
        excluded = {f".{e.lower().lstrip('.')}" for e in exclude.split(",")}

    # Find supported media files (sorted for deterministic order)
    media_files = sorted(
        [
            f
            for f in directory.iterdir()
            if f.is_file()
            and f.suffix.lower() in SUPPORTED_EXTENSIONS
            and f.suffix.lower() not in excluded
        ],
        key=lambda f: f.name,
    )

    if not media_files:
        print(f"No supported files found. Supported: {', '.join(SUPPORTED_EXTENSIONS)}")
        raise typer.Exit(1)

    if limit > 0:
        media_files = media_files[:limit]
    print(f"{'DRY RUN - ' if dry_run else ''}Processing {len(media_files)} files\n")

    success = 0
    for f in media_files:
        print(f"Processing: {f.name}")
        if dry_run:
            print(f"  Would add comment: '{comment}'")
            success += 1
        else:
            if add_comment(f, comment):
                print(f"  Added comment: '{comment}'")
                success += 1
            else:
                print(f"  Failed")

    print(f"\nDone. Modified {success}/{len(media_files)} files.")


if __name__ == "__main__":
    app()
