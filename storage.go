package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode"
)

type Bookmark struct {
	Alias    string    `json:"alias"`
	Path     string    `json:"path"`
	LastUsed time.Time `json:"last_used"`
}

func storeDirectory() (string, error) {
	if configHome, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok && configHome != "" {
		if !filepath.IsAbs(configHome) {
			return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
		}
		return filepath.Join(configHome, "bm"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the user's home directory: %w", err)
	}
	if home == "" {
		return "", errors.New("the user's home directory is empty")
	}
	return filepath.Join(home, ".config", "bm"), nil
}

func validateAlias(alias string) error {
	if alias == "" {
		return errors.New("alias must not be empty")
	}
	for _, character := range alias {
		if unicode.IsControl(character) {
			return errors.New("alias must not contain control characters")
		}
	}
	return nil
}

func addBookmark(alias string) error {
	if err := validateAlias(alias); err != nil {
		return err
	}
	currentDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get the current directory: %w", err)
	}

	return withStoreLock(true, func(directory string) error {
		bookmarks, err := readBookmarks(directory)
		if err != nil {
			return err
		}
		for _, bookmark := range bookmarks {
			if bookmark.Alias == alias {
				return fmt.Errorf("alias %q already exists", alias)
			}
		}

		bookmarks = append(bookmarks, Bookmark{
			Alias:    alias,
			Path:     currentDirectory,
			LastUsed: time.Now().UTC(),
		})
		if err := writeBookmarks(directory, bookmarks); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(os.Stdout, "%s -> %s\n", alias, sanitizeForDisplay(currentDirectory)); err != nil {
			return fmt.Errorf("print saved bookmark: %w", err)
		}
		return nil
	})
}

func loadBookmarksForSelector() ([]Bookmark, error) {
	var bookmarks []Bookmark
	err := withStoreLock(true, func(directory string) error {
		loaded, err := readBookmarks(directory)
		if err != nil {
			return err
		}

		remaining := make([]Bookmark, 0, len(loaded))
		removedStale := false
		for _, bookmark := range loaded {
			info, statErr := os.Stat(bookmark.Path)
			if os.IsNotExist(statErr) || (statErr == nil && !info.IsDir()) {
				removedStale = true
				continue
			}
			if statErr != nil {
				return fmt.Errorf("check bookmarked directory %q: %w", bookmark.Path, statErr)
			}
			remaining = append(remaining, bookmark)
		}

		if removedStale {
			if err := writeBookmarks(directory, remaining); err != nil {
				return err
			}
		}
		bookmarks = sortBookmarks(remaining)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return bookmarks, nil
}

func deleteBookmark(alias string) ([]Bookmark, error) {
	var remaining []Bookmark
	err := withStoreLock(true, func(directory string) error {
		bookmarks, err := readBookmarks(directory)
		if err != nil {
			return err
		}
		filtered := make([]Bookmark, 0, len(bookmarks))
		found := false
		for _, bookmark := range bookmarks {
			if bookmark.Alias == alias {
				found = true
				continue
			}
			filtered = append(filtered, bookmark)
		}
		if !found {
			return fmt.Errorf("bookmark %q no longer exists", alias)
		}
		if err := writeBookmarks(directory, filtered); err != nil {
			return err
		}
		remaining = sortBookmarks(filtered)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return remaining, nil
}

func markBookmarkUsed(alias string) error {
	return withStoreLock(true, func(directory string) error {
		bookmarks, err := readBookmarks(directory)
		if err != nil {
			return err
		}
		found := false
		for index := range bookmarks {
			if bookmarks[index].Alias == alias {
				bookmarks[index].LastUsed = time.Now().UTC()
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("bookmark %q no longer exists", alias)
		}
		return writeBookmarks(directory, bookmarks)
	})
}

func withStoreLock(exclusive bool, operation func(string) error) (result error) {
	directory, err := storeDirectory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create bookmark directory %q: %w", directory, err)
	}

	lockPath := filepath.Join(directory, "bookmarks.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open bookmark lock %q: %w", lockPath, err)
	}
	locked := false
	defer func() {
		if locked {
			if unlockErr := releaseStoreLock(lockFile); unlockErr != nil {
				result = errors.Join(result, fmt.Errorf("unlock bookmark store: %w", unlockErr))
			}
		}
		if closeErr := lockFile.Close(); closeErr != nil {
			result = errors.Join(result, fmt.Errorf("close bookmark lock: %w", closeErr))
		}
	}()

	if err := acquireStoreLock(lockFile, exclusive); err != nil {
		return fmt.Errorf("lock bookmark store: %w", err)
	}
	locked = true

	return operation(directory)
}

func readBookmarks(directory string) ([]Bookmark, error) {
	path := filepath.Join(directory, "bookmarks.json")
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []Bookmark{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read bookmarks from %q: %w", path, err)
	}

	var bookmarks []Bookmark
	if err := json.Unmarshal(content, &bookmarks); err != nil {
		return nil, fmt.Errorf("decode bookmark file %q: %w", path, err)
	}
	aliases := make(map[string]struct{}, len(bookmarks))
	for _, bookmark := range bookmarks {
		if err := validateAlias(bookmark.Alias); err != nil {
			return nil, fmt.Errorf("invalid alias in %q: %w", path, err)
		}
		if bookmark.Path == "" {
			return nil, fmt.Errorf("bookmark %q has an empty directory path", bookmark.Alias)
		}
		if _, exists := aliases[bookmark.Alias]; exists {
			return nil, fmt.Errorf("bookmark file contains duplicate alias %q", bookmark.Alias)
		}
		aliases[bookmark.Alias] = struct{}{}
	}
	return sortBookmarks(bookmarks), nil
}

func writeBookmarks(directory string, bookmarks []Bookmark) (result error) {
	path := filepath.Join(directory, "bookmarks.json")
	temporary, err := os.CreateTemp(directory, ".bookmarks-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary bookmark file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, fmt.Errorf("remove temporary bookmark file: %w", err))
		}
	}()

	if err := temporary.Chmod(0600); err != nil {
		return errors.Join(fmt.Errorf("set bookmark file permissions: %w", err), temporary.Close())
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(bookmarks); err != nil {
		return errors.Join(fmt.Errorf("encode bookmarks: %w", err), temporary.Close())
	}
	if err := temporary.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync temporary bookmark file: %w", err), temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary bookmark file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace bookmark file: %w", err)
	}

	directoryFile, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open bookmark directory for sync: %w", err)
	}
	syncErr := directoryFile.Sync()
	closeErr := directoryFile.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(
			wrapOptionalError("sync bookmark directory", syncErr),
			wrapOptionalError("close bookmark directory", closeErr),
		)
	}
	return nil
}

func wrapOptionalError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func sortBookmarks(bookmarks []Bookmark) []Bookmark {
	sorted := append([]Bookmark(nil), bookmarks...)
	sort.SliceStable(sorted, func(left, right int) bool {
		if sorted[left].LastUsed.Equal(sorted[right].LastUsed) {
			return sorted[left].Alias < sorted[right].Alias
		}
		return sorted[left].LastUsed.After(sorted[right].LastUsed)
	})
	return sorted
}
