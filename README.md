# nui

> [!NOTE]
> This project was in active development while my work laptop was a machine with
> little memory. Since that's not the case anymore, I don't plan to continue development.

A low-memory terminal UI for searching and reading [Notion](https://www.notion.com).

The Notion app is a comparative memory hog even when idle. If all you want is to
search & read, `nui` does that in a fraction of the footprint by gluing together
a few small native tools instead of a browser engine.

```
notion ❯ quarterly plan
┌─────────────────────────────────────────┬──────────────────────────────────┐
│ ▶ Q3 Planning                           │  Q3 Planning                     │
│   Quarterly Planning Notes              │  ────────────────                │
│   Plan: Marketing                       │  Goals                           │
│                                         │    1. Ship the new onboarding    │
│                                         │    2. Cut reader RAM in half     │
└─────────────────────────────────────────┴──────────────────────────────────┘
 Q3 Planning   ·  enter: read · ctrl-o: browser · ctrl-y: copy url · empty query = recents
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
It stores no credentials of its own, authentication lives entirely in `ntn`.

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

## Where the project stopped

Development stopped during a Go rewrite - the original mdcat/less pipeline was
too restrictive, whereas owning the viewport fully would allow me to do more.

The rewrite is technically usable under `nui-go` (since the original bash script
still occupies the `nui` command).

```sh
go build -o nui-go .
./nui-go whoami        # prints the authenticated Notion identity
./nui-go login         # wraps `ntn login` (browser OAuth)

go test ./...          # pure unit tests, no network
```

`nui-go` shells out to `ntn` for all Notion I/O (it stores no credentials of its
own) and degrades gracefully - e.g. with `ntn` missing it prints an install
nudge rather than a stack trace.

## License

[MIT](LICENSE).
