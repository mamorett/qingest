package discover

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoverMDFiles returns a sorted list of absolute paths matching extension under directory.
func DiscoverMDFiles(directory string, recursive bool, extension string) ([]string, error) {
	absDir, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for %s: %w", directory, err)
	}

	// Ensure directory exists
	info, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("failed to stat directory %s: %w", absDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", absDir)
	}

	if extension == "" {
		extension = ".md"
	}
	if !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}

	var results []string

	if recursive {
		err = filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), strings.ToLower(extension)) {
				results = append(results, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("error walking directory %s: %w", absDir, err)
		}
	} else {
		entries, err := os.ReadDir(absDir)
		if err != nil {
			return nil, fmt.Errorf("error reading directory %s: %w", absDir, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), strings.ToLower(extension)) {
				results = append(results, filepath.Join(absDir, entry.Name()))
			}
		}
	}

	sort.Strings(results)
	return results, nil
}
