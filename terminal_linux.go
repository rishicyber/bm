//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unsafe"
)

const (
	requestGetTermios = uintptr(syscall.TCGETS)
	requestSetTermios = uintptr(syscall.TCSETS)
	requestGetWindow  = uintptr(syscall.TIOCGWINSZ)
)

type terminalWindowSize struct {
	rows    uint16
	columns uint16
	xPixels uint16
	yPixels uint16
}

type terminalUI struct {
	file   *os.File
	colors bool
	rows   int
	cols   int
}

func selectBookmark(bookmarks []Bookmark, deleteEntry func(string) ([]Bookmark, error)) (selected *Bookmark, resultErr error) {
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open the controlling terminal; bm's selector requires an interactive terminal: %w", err)
	}
	defer func() {
		if err := terminal.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close controlling terminal: %w", err))
		}
	}()

	var original syscall.Termios
	if err := ioctlTermios(terminal.Fd(), requestGetTermios, &original); err != nil {
		return nil, fmt.Errorf("read terminal settings: %w", err)
	}
	active := original
	active.Iflag &^= syscall.IXON | syscall.ICRNL
	active.Oflag &^= syscall.OPOST
	active.Lflag &^= syscall.ECHO | syscall.ICANON
	active.Cc[syscall.VMIN] = 1
	active.Cc[syscall.VTIME] = 0
	if err := ioctlTermios(terminal.Fd(), requestSetTermios, &active); err != nil {
		return nil, fmt.Errorf("enable interactive terminal input: %w", err)
	}

	ui := terminalUI{
		file:   terminal,
		colors: terminalColorsEnabled(),
	}
	if err := ui.updateSize(); err != nil {
		restoreErr := ioctlTermios(terminal.Fd(), requestSetTermios, &original)
		return nil, errors.Join(fmt.Errorf("read terminal size: %w", err), restoreErr)
	}

	if _, err := terminal.Write([]byte("\x1b[?1049h\x1b[?25l")); err != nil {
		_, displayRestoreErr := terminal.Write([]byte("\x1b[0m\x1b[?25h\x1b[?1049l"))
		restoreErr := ioctlTermios(terminal.Fd(), requestSetTermios, &original)
		return nil, errors.Join(fmt.Errorf("start terminal selector: %w", err), displayRestoreErr, restoreErr)
	}
	screenActive := true
	settingsActive := true
	cleanup := func() error {
		var cleanupErrors []error
		if screenActive {
			if _, err := terminal.Write([]byte("\x1b[0m\x1b[?25h\x1b[?1049l")); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("restore terminal display: %w", err))
			}
			screenActive = false
		}
		if settingsActive {
			if err := ioctlTermios(terminal.Fd(), requestSetTermios, &original); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("restore terminal settings: %w", err))
			}
			settingsActive = false
		}
		return errors.Join(cleanupErrors...)
	}

	selected, runErr := runTerminalUI(&ui, bookmarks, deleteEntry)
	cleanupErr := cleanup()
	if runErr != nil || cleanupErr != nil {
		return nil, errors.Join(runErr, cleanupErr)
	}
	return selected, nil
}

func runTerminalUI(ui *terminalUI, bookmarks []Bookmark, deleteEntry func(string) ([]Bookmark, error)) (*Bookmark, error) {
	reader := keyReader{file: ui.file}
	selectedIndex := 0
	status := ""
	redraw := true
	signals := make(chan os.Signal, 2)
	signal.Notify(
		signals,
		os.Interrupt,
		syscall.SIGTERM,
		syscall.SIGQUIT,
		syscall.SIGHUP,
		syscall.SIGTSTP,
		syscall.SIGWINCH,
	)
	defer signal.Stop(signals)

	for {
		if redraw {
			if err := ui.render(bookmarks, selectedIndex, status); err != nil {
				return nil, fmt.Errorf("draw bookmark selector: %w", err)
			}
			redraw = false
		}

		select {
		case received := <-signals:
			if received == syscall.SIGWINCH {
				if err := ui.updateSize(); err != nil {
					return nil, fmt.Errorf("read terminal size after resize: %w", err)
				}
				redraw = true
				continue
			}
			return nil, fmt.Errorf("selector interrupted by %s", received)
		default:
		}

		key, available, err := reader.readKey()
		if err != nil {
			return nil, fmt.Errorf("read terminal input: %w", err)
		}
		if !available {
			continue
		}

		switch key {
		case "escape", "q":
			return nil, nil
		case "up", "k":
			if selectedIndex > 0 {
				selectedIndex--
				status = ""
				redraw = true
			}
		case "down", "j":
			if selectedIndex+1 < len(bookmarks) {
				selectedIndex++
				status = ""
				redraw = true
			}
		case "dd":
			if selectedIndex < len(bookmarks) {
				alias := bookmarks[selectedIndex].Alias
				remaining, err := deleteEntry(alias)
				if err != nil {
					return nil, err
				}
				bookmarks = remaining
				if selectedIndex >= len(bookmarks) && len(bookmarks) > 0 {
					selectedIndex = len(bookmarks) - 1
				}
				status = fmt.Sprintf("Deleted %s", alias)
				redraw = true
			}
		case "enter":
			if selectedIndex >= len(bookmarks) {
				status = "There is no bookmark selected."
				redraw = true
				continue
			}
			bookmark := bookmarks[selectedIndex]
			info, err := os.Stat(bookmark.Path)
			if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
				remaining, deleteErr := deleteEntry(bookmark.Alias)
				if deleteErr != nil {
					return nil, deleteErr
				}
				bookmarks = remaining
				if selectedIndex >= len(bookmarks) && len(bookmarks) > 0 {
					selectedIndex = len(bookmarks) - 1
				}
				status = fmt.Sprintf("Removed stale bookmark %s", bookmark.Alias)
				redraw = true
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("check bookmarked directory %q: %w", bookmark.Path, err)
			}
			return &bookmark, nil
		}
	}
}

func (ui *terminalUI) updateSize() error {
	var size terminalWindowSize
	if err := ioctlWindowSize(ui.file.Fd(), &size); err != nil {
		return err
	}
	ui.rows = int(size.rows)
	ui.cols = int(size.columns)
	if ui.rows < 1 || ui.cols < 1 {
		return errors.New("terminal reported an invalid size")
	}
	return nil
}

func (ui *terminalUI) render(bookmarks []Bookmark, selectedIndex int, status string) error {
	var output strings.Builder
	output.WriteString("\x1b[0m\x1b[2J\x1b[H")

	ui.writeLine(&output, 1, "  bm  /  Bookmarks", colorCode(183, ui.colors))
	ui.writeLine(&output, 2, "  Navigate to a saved working directory", colorCode(245, ui.colors))

	listTop := 4
	listBottom := ui.rows - 2
	visibleRows := listBottom - listTop + 1
	if visibleRows < 0 {
		visibleRows = 0
	}
	if len(bookmarks) == 0 {
		ui.writeLine(&output, listTop, "  No bookmarks yet. Add one with: bm <alias>", colorCode(151, ui.colors))
	} else if visibleRows > 0 {
		offset := 0
		if selectedIndex >= visibleRows {
			offset = selectedIndex - visibleRows + 1
		}
		innerWidth := ui.cols - 4
		if innerWidth < 1 {
			innerWidth = 1
		}
		aliasWidth := minInt(24, maxInt(8, innerWidth/3))
		if aliasWidth > innerWidth-5 {
			aliasWidth = maxInt(1, innerWidth/3)
		}
		pathWidth := innerWidth - aliasWidth - 4
		if pathWidth < 1 {
			pathWidth = 1
		}

		end := minInt(len(bookmarks), offset+visibleRows)
		for index := offset; index < end; index++ {
			row := listTop + index - offset
			alias := truncateRunes(sanitizeForDisplay(bookmarks[index].Alias), aliasWidth)
			path := middleTruncate(sanitizeForDisplay(bookmarks[index].Path), pathWidth)
			line := fmt.Sprintf("  %s%-*s  %s", "  ", aliasWidth, alias, path)
			if index == selectedIndex {
				line = fmt.Sprintf("  > %-*s  %s", aliasWidth, alias, path)
				if ui.colors {
					line = "\x1b[38;5;188;48;5;60m" + line + "\x1b[0m"
				}
				ui.writeRawLine(&output, row, line)
				continue
			}
			if ui.colors {
				line = fmt.Sprintf("  %s%-*s\x1b[0m  %s%s\x1b[0m",
					"\x1b[38;5;150m", aliasWidth, alias,
					"\x1b[38;5;245m", path)
			}
			ui.writeRawLine(&output, row, line)
		}
	}

	if status == "" && len(bookmarks) == 0 {
		status = "Use Escape or q to close."
	}
	if status != "" {
		ui.writeLine(&output, ui.rows-1, "  "+sanitizeForDisplay(status), colorCode(181, ui.colors))
	}
	ui.writeLine(&output, ui.rows, "  j/k or ↑/↓ move   Enter open   dd delete   q/Esc quit", colorCode(245, ui.colors))

	_, err := ui.file.Write([]byte(output.String()))
	return err
}

func (ui *terminalUI) writeLine(output *strings.Builder, row int, text string, color string) {
	if row < 1 || row > ui.rows {
		return
	}
	text = truncateRunes(sanitizeForDisplay(text), ui.cols)
	output.WriteString(cursorTo(row, 1))
	if color != "" {
		output.WriteString(color)
	}
	output.WriteString(text)
	output.WriteString("\x1b[0m\x1b[K")
}

func (ui *terminalUI) writeRawLine(output *strings.Builder, row int, text string) {
	if row < 1 || row > ui.rows {
		return
	}
	output.WriteString(cursorTo(row, 1))
	output.WriteString(text)
	output.WriteString("\x1b[0m\x1b[K")
}

func cursorTo(row, column int) string {
	return fmt.Sprintf("\x1b[%d;%dH", row, column)
}

func terminalColorsEnabled() bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled {
		return false
	}
	terminalType := os.Getenv("TERM")
	if terminalType == "" || terminalType == "dumb" {
		return false
	}
	colorMode := strings.ToLower(os.Getenv("COLORTERM"))
	return strings.Contains(terminalType, "256color") || colorMode == "truecolor" || colorMode == "24bit"
}

func colorCode(color int, enabled bool) string {
	if !enabled {
		return ""
	}
	return fmt.Sprintf("\x1b[38;5;%dm", color)
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	characters := []rune(value)
	if len(characters) <= limit {
		return value
	}
	return string(characters[:limit])
}

func middleTruncate(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	characters := []rune(value)
	if len(characters) <= limit {
		return value
	}
	if limit <= 3 {
		return string(characters[:limit])
	}
	available := limit - 3
	left := (available + 1) / 2
	right := available - left
	return string(characters[:left]) + "..." + string(characters[len(characters)-right:])
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func ioctlTermios(fileDescriptor uintptr, request uintptr, settings *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fileDescriptor, request, uintptr(unsafe.Pointer(settings)))
	if errno != 0 {
		return errno
	}
	return nil
}

func ioctlWindowSize(fileDescriptor uintptr, size *terminalWindowSize) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fileDescriptor, requestGetWindow, uintptr(unsafe.Pointer(size)))
	if errno != 0 {
		return errno
	}
	return nil
}

type keyReader struct {
	file   *os.File
	buffer []byte
}

func (reader *keyReader) readKey() (string, bool, error) {
	character, available, err := reader.readByte()
	if err != nil || !available {
		return "", available, err
	}

	switch character {
	case 0x1b:
		first, firstAvailable, err := reader.readByte()
		if err != nil {
			return "", false, err
		}
		if !firstAvailable {
			return "escape", true, nil
		}
		if first != '[' {
			reader.pushFront(first)
			return "escape", true, nil
		}
		second, secondAvailable, err := reader.readByte()
		if err != nil {
			return "", false, err
		}
		if !secondAvailable {
			return "escape", true, nil
		}
		switch second {
		case 'A':
			return "up", true, nil
		case 'B':
			return "down", true, nil
		default:
			reader.pushFront(second)
			return "escape", true, nil
		}
	case 'd':
		next, nextAvailable, err := reader.readByte()
		if err != nil {
			return "", false, err
		}
		if nextAvailable && next == 'd' {
			return "dd", true, nil
		}
		if nextAvailable {
			reader.pushFront(next)
		}
		return "d", true, nil
	case '\r', '\n':
		return "enter", true, nil
	case 'j', 'k', 'q':
		return string(character), true, nil
	default:
		return "", true, nil
	}
}

func (reader *keyReader) readByte() (byte, bool, error) {
	if len(reader.buffer) > 0 {
		character := reader.buffer[0]
		reader.buffer = reader.buffer[1:]
		return character, true, nil
	}
	fileDescriptor := int(reader.file.Fd())
	var readSet syscall.FdSet
	bitsPerWord := uint(unsafe.Sizeof(readSet.Bits[0]) * 8)
	word := uint(fileDescriptor) / bitsPerWord
	if word >= uint(len(readSet.Bits)) {
		return 0, false, errors.New("terminal file descriptor exceeds select capacity")
	}
	readSet.Bits[word] |= int64(1) << (uint(fileDescriptor) % bitsPerWord)
	timeout := syscall.Timeval{Sec: 0, Usec: 100000}
	ready, err := syscall.Select(fileDescriptor+1, &readSet, nil, nil, &timeout)
	if errors.Is(err, syscall.EINTR) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if ready == 0 {
		return 0, false, nil
	}
	var character [1]byte
	count, err := syscall.Read(fileDescriptor, character[:])
	if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if count == 0 {
		return 0, false, nil
	}
	return character[0], true, nil
}

func (reader *keyReader) pushFront(character byte) {
	reader.buffer = append([]byte{character}, reader.buffer...)
}
