// splitdir splits files from a directory into numbered subdirectories (1/, 2/, 3/, etc.).
// Uses first-fit-decreasing bin packing for efficient distribution.
// Files larger than the limit get their own directory.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

var (
	dryRun    bool
	splitSize string
)

func parseSize(sizeStr string) (int64, error) {
	re := regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*(B|KB|MB|GB|TB)?$`)
	match := re.FindStringSubmatch(sizeStr)
	if match == nil {
		return 0, fmt.Errorf("invalid size format: %q (expected format: number + unit, e.g., 8GB, 500MB, 1TB)", sizeStr)
	}

	num, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size format: %q", sizeStr)
	}

	unit := strings.ToUpper(match[2])
	if unit == "" {
		unit = "B"
	}

	multipliers := map[string]int64{
		"B":  1,
		"KB": 1024,
		"MB": 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
		"TB": 1024 * 1024 * 1024 * 1024,
	}

	return int64(num * float64(multipliers[unit])), nil
}

func formatSize(bytes int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(bytes)
	unitIndex := 0

	for size >= 1024 && unitIndex < len(units)-1 {
		size /= 1024
		unitIndex++
	}

	return fmt.Sprintf("%.2f %s", size, units[unitIndex])
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func moveFile(src, dest string) error {
	if fileExists(dest) {
		return fmt.Errorf("destination already exists: %s", dest)
	}

	err := os.Rename(src, dest)
	if err == nil {
		return nil
	}

	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		var errno syscall.Errno
		if errors.As(linkErr.Err, &errno) && errno == syscall.EXDEV {
			return copyAndDelete(src, dest)
		}
	}

	return err
}

func copyAndDelete(src, dest string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcStat, err := srcFile.Stat()
	if err != nil {
		return err
	}

	// Use O_EXCL to ensure no-clobber (fail if file exists)
	destFile, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, srcStat.Mode().Perm())
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		os.Remove(dest)
		return err
	}

	destFile.Close()
	srcFile.Close()

	destStat, err := os.Stat(dest)
	if err != nil {
		os.Remove(dest)
		return err
	}

	if srcStat.Size() != destStat.Size() {
		os.Remove(dest)
		return fmt.Errorf("copy verification failed: size mismatch for %s", src)
	}

	return os.Remove(src)
}

type fileInfo struct {
	name string
	size int64
}

// findMaxNumberedDir scans a directory for existing numbered subdirectories (1/, 2/, etc.)
// and returns the maximum number found. Returns 0 if no numbered directories exist.
func findMaxNumberedDir(sourceDir string) (int, error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return 0, err
	}

	numberedDirPattern := regexp.MustCompile(`^\d+$`)
	maxNum := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if numberedDirPattern.MatchString(name) {
			num, err := strconv.Atoi(name)
			if err != nil {
				continue
			}
			if num > maxNum {
				maxNum = num
			}
		}
	}

	return maxNum, nil
}

type operation struct {
	src     string
	dest    string
	dirName string
}

func splitDir(sourceDir string, maxSize int64, dryRun bool) error {
	// Find the maximum existing numbered directory to resume from
	startNum, err := findMaxNumberedDir(sourceDir)
	if err != nil {
		return fmt.Errorf("failed to scan existing directories: %w", err)
	}

	if startNum > 0 {
		fmt.Printf("Found existing numbered directories up to %d/, starting from %d/\n\n", startNum, startNum+1)
	}

	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return err
	}

	var files []fileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, fileInfo{name: entry.Name(), size: info.Size()})
	}

	if len(files) == 0 {
		fmt.Println("No files found in directory")
		return nil
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].size > files[j].size
	})

	var batches [][]fileInfo
	var batchSizes []int64

	for _, file := range files {
		if file.size > maxSize {
			fmt.Printf("Warning: %s exceeds %s (%s), placing in its own directory\n",
				file.name, formatSize(maxSize), formatSize(file.size))
			batches = append(batches, []fileInfo{file})
			batchSizes = append(batchSizes, file.size)
			continue
		}

		placed := false
		for i := range batches {
			if batchSizes[i]+file.size <= maxSize {
				batches[i] = append(batches[i], file)
				batchSizes[i] += file.size
				placed = true
				break
			}
		}

		if !placed {
			batches = append(batches, []fileInfo{file})
			batchSizes = append(batchSizes, file.size)
		}
	}

	fmt.Printf("Splitting %d files into %d directories (max %s each)\n\n",
		len(files), len(batches), formatSize(maxSize))

	var operations []operation

	for i, batch := range batches {
		// Start numbering from startNum+1 to avoid clobbering existing directories
		dirName := strconv.Itoa(startNum + i + 1)
		dirPath := filepath.Join(sourceDir, dirName)

		if dryRun {
			fmt.Printf("Directory %s: %d files (%s)\n", dirName, len(batch), formatSize(batchSizes[i]))
			for _, file := range batch {
				fmt.Printf("  %s\n", file.name)
			}
		}

		for _, file := range batch {
			operations = append(operations, operation{
				src:     filepath.Join(sourceDir, file.name),
				dest:    filepath.Join(dirPath, file.name),
				dirName: dirName,
			})
		}
	}

	if dryRun {
		fmt.Printf("\n%d files would be moved\n", len(operations))
		return nil
	}

	createdDirs := make(map[string]bool)
	completed := 0

	for _, op := range operations {
		dirPath := filepath.Join(sourceDir, op.dirName)

		if !createdDirs[dirPath] {
			if err := os.MkdirAll(dirPath, 0755); err != nil {
				return err
			}
			createdDirs[dirPath] = true
			fmt.Printf("\nDirectory %s:\n", op.dirName)
		}

		if err := moveFile(op.src, op.dest); err != nil {
			fmt.Printf("\nFAILED: %s -> %s\n", op.src, op.dest)
			fmt.Printf("Error: %v\n", err)
			fmt.Printf("\nStopping. %d/%d files moved.\n", completed, len(operations))
			fmt.Println("Re-run to continue with remaining files.")
			os.Exit(1)
		}

		fmt.Printf("  Moved: %s -> %s\n", op.src, op.dest)
		completed++
	}

	fmt.Printf("\nDone! %d files moved into %d directories.\n", completed, len(batches))
	return nil
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "splitdir <directory>",
		Short: "Split files into numbered subdirectories by size",
		Long: `splitdir splits files from a directory into numbered subdirectories (1/, 2/, 3/, etc.).

Uses first-fit-decreasing bin packing for efficient distribution of files.
Files larger than the size limit are placed in their own directory.

The tool supports cross-filesystem moves by automatically falling back to
copy-and-delete when rename fails across mount points.`,
		Example: `  # Split with default 8GB limit
  splitdir /path/to/files

  # Split with custom size limit
  splitdir --split-size 4GB /path/to/files
  splitdir -s 500MB /path/to/files

  # Preview changes without moving files
  splitdir --dry-run /path/to/files
  splitdir -n -s 2GB /path/to/files

Size format examples:
  100B    - 100 bytes
  500KB   - 500 kilobytes
  256MB   - 256 megabytes
  8GB     - 8 gigabytes (default)
  1TB     - 1 terabyte`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]

			// Validate directory exists
			info, err := os.Stat(dir)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("directory does not exist: %s", dir)
				}
				return fmt.Errorf("cannot access directory: %v", err)
			}
			if !info.IsDir() {
				return fmt.Errorf("not a directory: %s", dir)
			}

			// Parse size
			maxSize, err := parseSize(splitSize)
			if err != nil {
				return err
			}
			if maxSize <= 0 {
				return fmt.Errorf("split size must be positive, got: %s", splitSize)
			}

			if dryRun {
				fmt.Println("DRY RUN - no files will be moved")
				fmt.Println()
			}

			return splitDir(dir, maxSize, dryRun)
		},
	}

	rootCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "preview changes without moving files")
	rootCmd.Flags().StringVarP(&splitSize, "split-size", "s", "8GB", "size limit per subdirectory (e.g., 8GB, 500MB, 1TB)")

	return rootCmd
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
