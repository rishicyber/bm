# bm

`bm` is a Linux command-line bookmark utility written in Go. It uses only the
Go standard library and has no third-party module dependencies. Building
requires Go 1.20 or newer.

## Build and install

From the project directory on Linux:

```sh
mkdir -p "$HOME/.local/bin" "$HOME/.local/share/bm"
go build -o "$HOME/.local/bin/bm" .
install -m 0644 bm.bash "$HOME/.local/share/bm/bm.bash"
```

Ensure `~/.local/bin` is on `PATH`, then add this line to `~/.bashrc`:

```bash
source "$HOME/.local/share/bm/bm.bash"
```

Start a new Bash session or reload the configuration with `source ~/.bashrc`.
The Bash function is required because a child process cannot change its
parent shell's working directory.

## Use

```sh
bm "project alias"   # Save the current directory
bm                   # Open the interactive selector
```

Use `j`/`k` or the arrow keys to navigate, Enter to change to the selected
directory, `dd` to delete a bookmark, and Escape or `q` to exit. Bookmarks are
stored as JSON under `$XDG_CONFIG_HOME/bm` or, when that variable is unset,
`~/.config/bm`.

The selector uses Linux terminal controls directly. It runs in the current
terminal and requires a controlling interactive terminal.
