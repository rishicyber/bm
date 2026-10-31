//go:build !linux

package main

import "errors"

func selectBookmark(_ []Bookmark, _ func(string) ([]Bookmark, error)) (*Bookmark, error) {
	return nil, errors.New("the interactive selector is supported only on Linux")
}
