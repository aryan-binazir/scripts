// consolidatefiles moves all files from one or more source directories into a single target directory.
// Recursively scans subdirectories and flattens the structure.
// Handles filename collisions by appending a numeric suffix (e.g., photo_1.jpg).
// Supports cross-filesystem moves (copy + delete with verification).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type operation struct {
	src     string
	dest    string
	renamed bool
}

// claimedNames tracks names that have been assigned to accurately predict dry-run destinations
var claimedNames = make(map[string]struct{})

// flags
var dryRun bool
var verifyChecksum bool

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "consolidatefiles <target-dir> <source-dir>...",
	Short: "Consolidate files from multiple directories into one",
	Long: `Consolidate files from multiple source directories into a single target directory.

Recursively scans all source directories and moves files into the target,
flattening the directory structure. Handles filename collisions by appending
a numeric suffix (e.g., photo.jpg becomes photo_1.jpg).

Supports cross-filesystem moves by automatically falling back to copy + delete
with verification when source and target are on different filesystems.`,
	Example: `  # Preview what would happen (recommended first step)
  consolidatefiles --dry-run 'Vacation Photos' 114APPLE 115APPLE

  # Move files from multiple camera folders into one
  consolidatefiles 'Vacation Photos' 114APPLE 115APPLE 116APPLE

  # Consolidate downloads into a single folder
  consolidatefiles ~/Documents/Archive ~/Downloads/batch1 ~/Downloads/batch2`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return fmt.Errorf("missing required argument: target-dir")
		}
		if len(args) < 2 {
			return fmt.Errorf("missing required argument: at least one source-dir is required")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		targetDir := args[0]
		sourceDirs := args[1:]

		if dryRun {
			fmt.Print("DRY RUN - no files will be moved\n\n")
		}

		return run(targetDir, sourceDirs, dryRun)
	},
}

func init() {
	rootCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "preview changes without moving any files")
	rootCmd.Flags().BoolVar(&verifyChecksum, "verify", false, "verify SHA256 checksums after copy (slower but safer)")
}

// checkPathOverlap detects unsafe path relationships between target and source directories.
// Returns an error if:
// - targetDir equals any sourceDir
// - targetDir is inside any sourceDir (would move target into itself)
// - any sourceDir is inside targetDir (would recursively process moved files)
func checkPathOverlap(targetDir string, sourceDirs []string) error {
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("failed to resolve target path: %w", err)
	}
	absTarget = filepath.Clean(absTarget)

	for _, src := range sourceDirs {
		absSrc, err := filepath.Abs(src)
		if err != nil {
			return fmt.Errorf("failed to resolve source path %q: %w", src, err)
		}
		absSrc = filepath.Clean(absSrc)

		// Check if paths are identical
		if absTarget == absSrc {
			return fmt.Errorf("path overlap detected: target directory %q is the same as source directory %q", targetDir, src)
		}

		// Check if target is inside source (would cause recursive issues)
		if isSubPath(absTarget, absSrc) {
			return fmt.Errorf("path overlap detected: target directory %q is inside source directory %q", targetDir, src)
		}

		// Check if source is inside target (would process already-moved files)
		if isSubPath(absSrc, absTarget) {
			return fmt.Errorf("path overlap detected: source directory %q is inside target directory %q", src, targetDir)
		}
	}

	return nil
}

// isSubPath returns true if child is a subdirectory of parent.
// Both paths must be absolute and cleaned.
func isSubPath(child, parent string) bool {
	// Ensure parent ends with separator for accurate prefix matching
	parentWithSep := parent
	if !strings.HasSuffix(parentWithSep, string(filepath.Separator)) {
		parentWithSep += string(filepath.Separator)
	}
	return strings.HasPrefix(child, parentWithSep)
}

func run(targetDir string, sourceDirs []string, dryRun bool) error {
	// Safety check: detect overlapping paths before any work
	if err := checkPathOverlap(targetDir, sourceDirs); err != nil {
		return err
	}

	// Pre-populate claimedNames with existing files in target
	entries, err := os.ReadDir(targetDir)
	if err == nil {
		for _, entry := range entries {
			claimedNames[filepath.Join(targetDir, entry.Name())] = struct{}{}
		}
	}
	// Target doesn't exist yet, that's fine

	// Ensure target directory exists
	if !dryRun {
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return fmt.Errorf("failed to create target directory: %w", err)
		}
	}

	// First pass: collect all operations
	var operations []operation

	for _, sourceDir := range sourceDirs {
		files, err := getAllFiles(sourceDir)
		if err != nil {
			fmt.Printf("Skipping %s: not found or not accessible\n", sourceDir)
			continue
		}

		for _, filePath := range files {
			fileName := filepath.Base(filePath)
			simpleDest := filepath.Join(targetDir, fileName)
			finalDest := getUniqueName(targetDir, fileName)
			wasRenamed := finalDest != simpleDest

			operations = append(operations, operation{
				src:     filePath,
				dest:    finalDest,
				renamed: wasRenamed,
			})
		}
	}

	if len(operations) == 0 {
		fmt.Println("No files found to move.")
		return nil
	}

	// Show plan and count renamed
	var renamed int
	for _, op := range operations {
		if op.renamed {
			renamed++
		}
		if dryRun {
			suffix := ""
			if op.renamed {
				suffix = " (renamed)"
			}
			fmt.Printf("Would move: %s -> %s%s\n", op.src, op.dest, suffix)
		}
	}

	if dryRun {
		plural := "s"
		if len(operations) == 1 {
			plural = ""
		}
		fmt.Printf("\n%d file%s would be moved (%d renamed to avoid duplicates)\n", len(operations), plural, renamed)
		return nil
	}

	// Execute moves
	if verifyChecksum {
		fmt.Println("Checksum verification enabled (SHA256)")
	}
	var completed int
	for _, op := range operations {
		if err := moveFile(op.src, op.dest, verifyChecksum); err != nil {
			fmt.Fprintf(os.Stderr, "\nFAILED: %s -> %s\n", op.src, op.dest)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			fmt.Fprintf(os.Stderr, "\nStopping. %d/%d files moved successfully.\n", completed, len(operations))
			fmt.Fprintf(os.Stderr, "Re-run the script to continue with remaining files.\n")
			os.Exit(1)
		}
		suffix := ""
		if op.renamed {
			suffix = " (renamed)"
		}
		fmt.Printf("Moved: %s -> %s%s\n", op.src, op.dest, suffix)
		completed++
	}

	plural := "s"
	if completed == 1 {
		plural = ""
	}
	fmt.Printf("\n%d file%s moved (%d renamed to avoid duplicates)\n", completed, plural, renamed)
	return nil
}

func getAllFiles(dir string) ([]string, error) {
	var files []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			subFiles, err := getAllFiles(fullPath)
			if err != nil {
				return nil, err
			}
			files = append(files, subFiles...)
		} else if entry.Type().IsRegular() {
			files = append(files, fullPath)
		}
	}

	return files, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func getUniqueName(targetDir, fileName string) string {
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	candidate := filepath.Join(targetDir, fileName)
	counter := 1

	for fileExists(candidate) || isClaimed(candidate) {
		candidate = filepath.Join(targetDir, fmt.Sprintf("%s_%d%s", base, counter, ext))
		counter++
	}

	claimedNames[candidate] = struct{}{}
	return candidate
}

func isClaimed(path string) bool {
	_, ok := claimedNames[path]
	return ok
}

func moveFile(src, dest string, verify bool) error {
	// No-clobber check: verify destination doesn't exist immediately before rename
	if fileExists(dest) {
		return fmt.Errorf("destination file already exists (no-clobber): %s", dest)
	}

	// Try atomic rename first
	err := os.Rename(src, dest)
	if err == nil {
		return nil
	}

	// Check if it's a cross-filesystem error
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) && isCrossDevice(linkErr) {
		// For cross-filesystem moves with verification, compute source checksum first
		var srcChecksum string
		if verify {
			var err error
			srcChecksum, err = computeSHA256(src)
			if err != nil {
				return fmt.Errorf("failed to compute source checksum: %w", err)
			}
		}

		// Cross-filesystem: copy then delete
		if err := copyFile(src, dest); err != nil {
			return fmt.Errorf("copy failed: %w", err)
		}

		// Verify copy succeeded before deleting
		srcInfo, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("failed to stat source: %w", err)
		}
		destInfo, err := os.Stat(dest)
		if err != nil {
			return fmt.Errorf("failed to stat destination: %w", err)
		}
		if srcInfo.Size() != destInfo.Size() {
			return fmt.Errorf("copy verification failed: size mismatch for %s", src)
		}

		// SHA256 verification if enabled
		if verify {
			destChecksum, err := computeSHA256(dest)
			if err != nil {
				return fmt.Errorf("failed to compute destination checksum: %w", err)
			}
			if srcChecksum != destChecksum {
				// Remove the corrupted copy
				os.Remove(dest)
				return fmt.Errorf("checksum verification failed for %s: source=%s dest=%s", src, srcChecksum, destChecksum)
			}
			fmt.Printf("  [verified] SHA256: %s\n", srcChecksum)
		}

		if err := os.Remove(src); err != nil {
			return fmt.Errorf("failed to remove source after copy: %w", err)
		}
		return nil
	}

	return err
}

func isCrossDevice(err *os.LinkError) bool {
	// Check for EXDEV error (cross-device link)
	// The error string varies by OS but typically contains "cross-device" or "invalid cross-device link"
	errStr := err.Error()
	return strings.Contains(errStr, "cross-device") ||
		strings.Contains(errStr, "EXDEV") ||
		strings.Contains(errStr, "invalid cross-device link")
}

// computeSHA256 calculates the SHA256 checksum of a file and returns it as a hex string.
func computeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dest string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	// Use O_EXCL to fail if destination already exists (no-clobber)
	destFile, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, srcInfo.Mode()&fs.ModePerm)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("destination file already exists (no-clobber): %s", dest)
		}
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		os.Remove(dest)
		return err
	}

	// Ensure data is flushed to disk
	if err := destFile.Sync(); err != nil {
		os.Remove(dest)
		return err
	}

	return nil
}
