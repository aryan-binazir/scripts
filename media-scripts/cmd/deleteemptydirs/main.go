// delete-empty-dirs
//
// Removes empty directories from a specified root directory.
// Only scans one level deep (immediate subdirectories).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	dryRun  bool
	rootDir string
)

var rootCmd = &cobra.Command{
	Use:   "deleteemptydirs",
	Short: "Remove empty directories from a target directory",
	Long: `deleteemptydirs scans a target directory for empty subdirectories
and removes them. Only immediate subdirectories (one level deep) are checked.

By default, the current working directory is used. Use --root to specify
a different target directory.

The command is useful for cleaning up directory structures after file operations
that may leave behind empty folders.`,
	Example: `  # Delete all empty directories in the current folder
  deleteemptydirs

  # Delete empty directories in a specific path
  deleteemptydirs --root /path/to/directory
  deleteemptydirs -r /path/to/directory

  # Preview what would be deleted without making changes
  deleteemptydirs --dry-run
  deleteemptydirs -n

  # Combine flags
  deleteemptydirs --root /path/to/directory --dry-run`,
	Args: cobra.NoArgs,
	RunE: run,
}

func init() {
	rootCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "preview changes without deleting any directories")
	rootCmd.Flags().StringVarP(&rootDir, "root", "r", ".", "root directory to scan for empty subdirectories")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	targetDir := filepath.Clean(rootDir)

	// Validate that the path exists and is a directory
	info, err := os.Stat(targetDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("path does not exist: %s", targetDir)
		}
		return fmt.Errorf("error accessing path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", targetDir)
	}

	if dryRun {
		fmt.Print("DRY RUN - no directories will be deleted\n\n")
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return fmt.Errorf("error reading directory: %w", err)
	}

	count := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		path := filepath.Join(targetDir, entry.Name())
		contents, err := os.ReadDir(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", path, err)
			continue
		}

		if len(contents) == 0 {
			if dryRun {
				fmt.Printf("Would delete: %s\n", entry.Name())
			} else {
				if err := os.Remove(path); err != nil {
					fmt.Fprintf(os.Stderr, "Error deleting %s: %v\n", path, err)
					continue
				}
				fmt.Printf("Deleted: %s\n", entry.Name())
			}
			count++
		}
	}

	action := "removed"
	if dryRun {
		action = "would be removed"
	}

	suffix := "ies"
	if count == 1 {
		suffix = "y"
	}

	fmt.Printf("\n%d empty director%s %s\n", count, suffix, action)
	return nil
}
