# rcmd

[日本語](README.ja.md)

A small personal project: a terminal UI for managing bash keyboard shortcuts
(`bind -x`). You register a command, pick a Ctrl/Alt key for it, and it shows
up in a nano-style TUI where you can browse, edit, or remove it — without
having to remember `source ~/.bashrc` every time.

Built mostly to scratch my own itch, so expect rough edges. Issues and PRs
are welcome.

> **Note:** rcmd works with bash only (it relies on bash's `bind -x` and
> readline). It has only been tested on Ubuntu so far.

## What it does

- Bind a shell command to a Ctrl/Alt key (including two-stroke combos like
  `Ctrl+X` → `5`)
- Add, edit, delete, and search shortcuts from one TUI
- Import/export your shortcut set as JSON
- Tries to warn you before you overwrite a key that's already in use —
  either by another rcmd shortcut or by a default readline binding (e.g.
  `Ctrl+R` for history search) — using `bind -X` to check what's actually
  active in your shell
- Won't let you bind `Ctrl+C` / `Ctrl+D` / `Ctrl+Z`, since those already
  belong to the shell (SIGINT / EOF / SIGTSTP)
- Once set up, changes take effect right away — no manual
  `source ~/.bashrc` needed

## Requirements

- Go 1.24+ (to build)
- bash

## Install

```bash
git clone https://github.com/sltomato/rcmd.git
cd rcmd
go build -o rcmd .
```

Put the resulting `rcmd` binary somewhere on your `PATH`, e.g.:

```bash
sudo mv rcmd /usr/local/bin/
```

## Setup

Run this once:

```bash
rcmd init
```

This adds a small block to your `~/.bashrc`:

- `eval "$(rcmd export)"` — loads your registered shortcuts as real
  `bind -x` bindings
- a `rcmd` shell function that wraps the binary so that running
  `rcmd ls` automatically re-sources `~/.bashrc` afterward, applying any
  changes you made without you having to do it yourself

Apply it to your current shell once:

```bash
source ~/.bashrc
```

After that, `rcmd ls` will keep itself in sync automatically.

## Usage

```bash
rcmd ls
```

Opens the TUI. From there:

| Key | Action |
|---|---|
| `Up`/`Down` | Move |
| `Ctrl+N` | Add a new shortcut |
| `Ctrl+E` | Edit the selected shortcut |
| `Ctrl+D` | Delete the selected shortcut |
| `Ctrl+W` | Search |
| `Ctrl+O` | Export shortcuts to a JSON file |
| `Ctrl+R` | Import shortcuts from a JSON file |
| `Ctrl+Q` | Quit |
| Mouse click | Select a row / click `[Edit]` `[Delete]` on the selected row |
| Mouse wheel | Scroll |

When adding a shortcut, press the key you want to bind, then confirm on the
preview screen (press any other key to try a different binding instead).

## How it works

Shortcuts are stored in `~/.config/rcmd/config.json`. `rcmd export` turns
that into `bind -x` commands, which `eval "$(rcmd export)"` (added to your
`~/.bashrc` by `rcmd init`) loads into bash on every new shell.

## License

MIT — see [LICENSE](LICENSE).

---

If you end up building something new out of this, I'd genuinely love to
hear about it — totally optional, just something that would make my day.
