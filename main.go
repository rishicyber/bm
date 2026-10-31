package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "bm: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 0 && os.Getenv("BM_INTERNAL_SELECT") == "1":
		return runShellSelector()
	case len(args) == 1 && os.Getenv("BM_INTERNAL_MARK_USED") == "1":
		if err := validateAlias(args[0]); err != nil {
			return err
		}
		return markBookmarkUsed(args[0])
	case len(args) == 0:
		selected, err := openSelector()
		if err != nil {
			return err
		}
		if selected != nil {
			if _, err := fmt.Fprintf(os.Stdout, "Selected directory: %q\n", selected.Path); err != nil {
				return fmt.Errorf("print selected directory: %w", err)
			}
		}
		return nil
	case len(args) == 1:
		return addBookmark(args[0])
	default:
		return errors.New("usage: bm [alias]")
	}
}

func runShellSelector() error {
	selected, err := openSelector()
	if err != nil {
		return err
	}

	writer := os.Stdout
	if selected == nil {
		_, err = writer.Write([]byte("cancelled\x00"))
		if err != nil {
			return fmt.Errorf("write cancellation response for Bash integration: %w", err)
		}
		return nil
	}
	if _, err = fmt.Fprint(writer, "selected\x00", selected.Alias, "\x00", selected.Path, "\x00"); err != nil {
		return fmt.Errorf("write selection for Bash integration: %w", err)
	}
	return nil
}

func openSelector() (*Bookmark, error) {
	bookmarks, err := loadBookmarksForSelector()
	if err != nil {
		return nil, err
	}
	return selectBookmark(bookmarks, deleteBookmark)
}

func sanitizeForDisplay(value string) string {
	var output strings.Builder
	for _, character := range value {
		if !unicode.IsControl(character) {
			output.WriteRune(character)
			continue
		}
		if character <= 0xff {
			fmt.Fprintf(&output, "\\x%02x", character)
		} else {
			fmt.Fprintf(&output, "\\u%04x", character)
		}
	}
	return output.String()
}
