// Package platform holds the tiny, OS-specific shell-outs nui needs from the
// TUI: opening a URL in the browser (ctrl-o) and copying a URL to the clipboard
// (ctrl-y). These are Go ports of the bash `nui` `open`/`pbcopy` bindings,
// kept behind this helper so the TUI never embeds OS branching.
//
// It depends on nothing in nui (no doc/notion/tui import) and never panics: a
// missing tool or a non-zero exit is returned as an error the caller surfaces
// in the status line.
package platform

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// actionTimeout bounds the helper exec so a hung `open`/`pbcopy` cannot wedge
// the TUI event loop (these are fire-and-forget UX actions).
const actionTimeout = 5 * time.Second

// OpenURL opens url in the user's default browser. It is a no-op-with-error if
// url is empty so the caller can report "no URL for this item" uniformly.
func OpenURL(url string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("no URL to open")
	}
	name, args := openCommand(url)
	if name == "" {
		return fmt.Errorf("open: unsupported platform %q", runtime.GOOS)
	}
	return runSilent(name, args, "")
}

// CopyURL copies url to the system clipboard. Empty url is an error, mirroring
// OpenURL.
func CopyURL(url string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("no URL to copy")
	}
	name, args := copyCommand()
	if name == "" {
		return fmt.Errorf("copy: unsupported platform %q", runtime.GOOS)
	}
	return runSilent(name, args, url)
}

// openCommand returns the browser-open command for the current OS. darwin uses
// `open`; linux prefers `xdg-open`. Other platforms return "" (unsupported).
func openCommand(url string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{url}
	case "linux":
		return "xdg-open", []string{url}
	default:
		return "", nil
	}
}

// copyCommand returns the clipboard command and whether it reads stdin. darwin
// uses `pbcopy`; linux prefers `wl-copy`, falling back to `xclip` when wl-copy
// is absent. The chosen command always reads the payload from stdin (see
// runSilent's stdin arg).
func copyCommand() (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "pbcopy", nil
	case "linux":
		if _, err := exec.LookPath("wl-copy"); err == nil {
			return "wl-copy", nil
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			return "xclip", []string{"-selection", "clipboard"}
		}
		return "", nil
	default:
		return "", nil
	}
}

// runSilent runs name with args, feeding stdin when non-empty, discarding
// stdout, and surfacing a non-zero exit as an error. It is timeout-bounded so a
// hung helper cannot block the TUI.
func runSilent(name string, args []string, stdin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
