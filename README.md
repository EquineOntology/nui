# nui

A low-memory terminal UI for searching and reading [Notion](https://www.notion.com).

The Notion desktop app is an Electron client that can hold ~1 GB of RAM resident
even when idle. If all you want is to **search and read** your pages, `nui` does
that in a fraction of the footprint — typically ~13 MB idle, ~50 MB peak — by
gluing together a few small native tools instead of a browser engine.

It is a **reader**, not an editor: there is no Electron, and nothing is written
back to Notion.

```
notion ❯ quarterly plan
┌─────────────────────────────────────────┬──────────────────────────────────┐
│ ▶ Q3 Planning                            │  Q3 Planning                     │
│   Quarterly Planning Notes               │  ────────────────                │
│   Plan: Marketing                        │  Goals                           │
│                                          │    1. Ship the new onboarding    │
│                                          │    2. Cut reader RAM in half     │
└─────────────────────────────────────────┴──────────────────────────────────┘
 Q3 Planning   ·  enter: read · ctrl-o: browser · ctrl-y: copy url · empty query = recents
```

## How it works

`nui` is a single Bash script that orchestrates four native binaries:

| Tool | Role |
| --- | --- |
| [`ntn`](https://www.notion.com/product/dev) | the data source — Notion's official CLI (search, fetch pages/databases) |
| [`fzf`](https://github.com/junegunn/fzf) | the interactive search/list UI and the database row picker |
| [`mdcat`](https://github.com/swsnr/mdcat) | renders Markdown to styled ANSI (real headings, bold/italic, tables, links) |
| `python3` | small text filters (search ranking, Markdown cleanup, table layout) |

Pages render full-screen in `less`; results and previews live in `fzf`.

## Requirements

- `ntn` — the Notion CLI, authenticated (`ntn login`)
- `fzf` (≥ 0.62, for footer support)
- `mdcat`
- `python3`
- `less` (≥ 668 recommended, for clickable links in the reader)

### macOS (Homebrew)

```sh
brew install fzf mdcat
brew install --cask notion-cli   # provides the `ntn` binary
```

### Linux

```sh
# fzf and python3 from your package manager; mdcat via cargo or your distro:
cargo install mdcat          # or: apt/dnf/pacman install mdcat, where packaged
# ntn (Notion CLI): distributed as a binary — see https://www.notion.com/product/dev
```

## Install

```sh
git clone https://github.com/EQuineOntology/nui.git
cd nui

# Authenticate with your own Notion account (opens a browser):
ntn login

# Put nui on your PATH — a symlink keeps it in sync with the repo:
ln -s "$PWD/nui" ~/.local/bin/nui        # ensure ~/.local/bin is on $PATH
```

`nui` only ever sees the pages your `ntn login` session is granted access to.
It stores no credentials of its own — authentication lives entirely in `ntn`.

## Usage

```sh
nui                # interactive UI; an empty query shows your recently-read pages
nui quarterly      # start with an initial search
```

### Controls

| Key | Action |
| --- | --- |
| type | search Notion by title (live) |
| ↑ / ↓ | move selection (preview updates) |
| enter | open the selected page full-screen in the reader |
| ctrl-o | open the selected page in your browser |
| ctrl-y | copy the selected page URL |
| ctrl-f / ctrl-b | scroll the preview pane |
| esc / ctrl-c | quit |

Opening a **database** drops you into a row-by-row picker; pick a row to read
that entry's page. Pages you open are remembered (most-recent first), so
launching `nui` with an empty query brings up your recents.

### Notes & limitations

- **Search is title-only.** Notion's public API exposes only a title search
  (`/v1/search`); there is no full-text or semantic search. `nui` re-ranks
  results client-side by how well the title matches your query, and sorts by
  last-edited time, which surfaces the pages you actually touch.
- It reads what your Notion integration/login can see — nothing more.

## Configuration

All optional, via environment variables:

| Variable | Default | Effect |
| --- | --- | --- |
| `NUI_NEST` | `1` | indent content by heading depth; set `0` to disable |
| `XDG_STATE_HOME` | `~/.local/state` | where the recents list is stored (`nui/recents.tsv`) |
| `TMPDIR` | `/tmp` | where the per-session render cache lives |
| `NUI_OUTLINE_FILE` | _(unset)_ | if set, the reader pipeline also writes a heading-outline JSON sidecar here (`[{level,text,line}]`) — a hook for richer readers |

## License

[MIT](LICENSE).
