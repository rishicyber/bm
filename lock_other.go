//go:build !linux

package main

import (
	"errors"
	"os"
)

func acquireStoreLock(_ *os.File, _ bool) error {
	return errors.New("bookmarks is supported only on Linux")
}

func releaseStoreLock(_ *os.File) error {
	return nil
}
