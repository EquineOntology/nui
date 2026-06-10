// Command nui is a low-memory terminal reader for Notion. It searches and reads
// Notion content through the `ntn` CLI; it never edits (writes are far-future)
// and stores no secrets of its own.
//
// main.go is a deliberately flat argument dispatch — no cobra. The CLI surface
// is tiny and stays tiny; a framework would be pure overhead against the
// single-binary / low-memory reason this project exists (SPEC §3, G0 notes).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/notion"
	"github.com/EQuineOntology/nui/internal/state"
)

const usage = `nui — a low-memory terminal reader for Notion

Usage:
  nui                 search Notion interactively (shows recents when empty)
  nui <query>         search Notion for <query>
  nui read <id>       open a page or database by id
  nui dump <id>       print a page's Document IR as an indented text tree
  nui dump <id> --json
                      print a page's raw block-tree JSON (fixture capture)
  nui inventory       tally block-type frequencies + coverage for your recents
  nui inventory <id>...
                      tally block-type frequencies for the given page ids
  nui login           authenticate with Notion (via ntn)
  nui whoami          show the authenticated identity
`

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches on the first argument and returns a process exit code. It is
// split out from main so the dispatch is straightforward to reason about and,
// later, to test.
func run(args []string) int {
	ctx := context.Background()

	// Bare `nui` and `nui <query>` both fall through to the search path; only
	// the reserved subcommands (login/whoami/read) and -h/--help branch away.
	switch first(args) {
	case "login":
		return cmdLogin(ctx)
	case "whoami":
		return cmdWhoAmI(ctx)
	case "read":
		return cmdRead(ctx, args[1:])
	case "dump":
		return cmdDump(ctx, args[1:])
	case "inventory":
		return cmdInventory(ctx, args[1:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	default:
		// Bare invocation or a free-text query: the interactive search list.
		return cmdSearch(ctx, args)
	}
}

// first returns args[0] or "" for an empty slice, so the dispatch switch reads
// cleanly without a length guard.
func first(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func cmdLogin(ctx context.Context) int {
	if err := notion.Login(ctx); err != nil {
		return fail(err)
	}
	return 0
}

func cmdWhoAmI(ctx context.Context) int {
	id, err := notion.NewClient().WhoAmI(ctx)
	if err != nil {
		return fail(err)
	}
	fmt.Println(id.String())
	return 0
}

// cmdRead is the future Go reader entry point (G2). Dispatch routes here now so
// later phases only fill in the body; today it is an honest stub.
func cmdRead(_ context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "nui read: missing page id\n\nusage: nui read <id>")
		return 2
	}
	fmt.Fprintf(os.Stderr, "nui read %s: not yet implemented (G2)\n", args[0])
	return 1
}

// cmdDump implements `nui dump <id> [--json]` (SPEC G1 deliverable 6). With
// --json it prints the raw cached block-tree JSON — the way fixtures are captured
// into testdata/ and proof the tolerant decoder round-trips. Without it, it
// prints the built Document as a deterministic indented text tree, a human
// parity check against what Notion shows.
func cmdDump(ctx context.Context, args []string) int {
	id, jsonOut, ok := parseDumpArgs(args)
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: nui dump <id> [--json]")
		return 2
	}
	c := notion.NewClient()

	if jsonOut {
		raw, err := c.RawDocumentJSON(ctx, id)
		if err != nil {
			return fail(err)
		}
		if _, werr := os.Stdout.Write(raw); werr != nil {
			return fail(werr)
		}
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			fmt.Println()
		}
		return 0
	}

	d, err := c.Document(ctx, id)
	if err != nil {
		return fail(err)
	}
	fmt.Print(doc.Dump(d))
	return 0
}

// parseDumpArgs extracts the id and the --json flag in any order.
func parseDumpArgs(args []string) (id string, jsonOut, ok bool) {
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			if id == "" {
				id = a
			}
		}
	}
	return id, jsonOut, id != ""
}

// cmdInventory implements `nui inventory [--recents|<id>...]` (SPEC G1
// deliverable 7). It walks the given pages (default: the recents list), tallies
// block-type frequencies, and prints a coverage verdict that drives G2/G3
// triage. A page that fails to fetch is reported to stderr but does not abort
// the run — partial coverage data still beats none.
func cmdInventory(ctx context.Context, args []string) int {
	ids, err := inventoryIDs(args)
	if err != nil {
		return fail(err)
	}
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "nui inventory: no pages (recents empty; pass page ids)")
		return 1
	}

	c := notion.NewClient()
	inv := doc.NewInventory()
	for _, id := range ids {
		d, derr := c.Document(ctx, id)
		if derr != nil {
			fmt.Fprintf(os.Stderr, "nui inventory: skipping %s: %v\n", id, derr)
			continue
		}
		inv.AddDocument(d)
	}
	fmt.Print(inv.Report())
	return 0
}

// inventoryIDs resolves the page ids to inventory: explicit ids when given
// (ignoring a leading --recents flag), otherwise the recents list.
func inventoryIDs(args []string) ([]string, error) {
	var ids []string
	for _, a := range args {
		if a == "--recents" {
			continue
		}
		ids = append(ids, a)
	}
	if len(ids) > 0 {
		return ids, nil
	}
	recents, err := state.Load()
	if err != nil {
		return nil, err
	}
	return state.IDs(recents), nil
}

// cmdSearch is the future interactive list (G4, currently fronted by the bash
// nui). Dispatch routes here now; today it is an honest stub.
func cmdSearch(_ context.Context, _ []string) int {
	fmt.Fprintln(os.Stderr, "nui search: not yet implemented (G4)")
	return 1
}

// fail prints a typed notion error as an actionable, no-stack-trace message and
// returns the process exit code. This is the single place CLI error UX lives.
func fail(err error) int {
	switch {
	case errors.Is(err, notion.ErrNtnNotFound):
		fmt.Fprint(os.Stderr, "nui: "+err.Error()+"\n\n")
		fmt.Fprint(os.Stderr, "    curl -fsSL https://ntn.dev | bash\n")
	case errors.Is(err, notion.ErrUnauthorized):
		fmt.Fprintln(os.Stderr, "nui: not logged in. Run: nui login")
	default:
		fmt.Fprintln(os.Stderr, "nui: "+err.Error())
	}
	return 1
}
