package notion

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Login runs `ntn login`, inheriting the parent's stdio so ntn can drive the
// browser OAuth flow and any interactive prompts directly. nui stores no
// secrets of its own — auth lives entirely in ntn (SPEC §1, §3). It does not go
// through Client.run because the rate limiter, timeout, and stdout capture are
// all wrong for an interactive, long-lived browser flow.
func Login(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "ntn", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var execErr *exec.Error
		if errors.Is(err, exec.ErrNotFound) ||
			(errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound)) {
			return ErrNtnNotFound
		}
		return fmt.Errorf("ntn login failed: %w", err)
	}
	return nil
}

// WhoAmI runs `ntn whoami --json` (the raw /v1/users/me response) and resolves
// it into a display-ready Identity via the shared tolerant decoder. If ntn is
// absent it returns ErrNtnNotFound; if ntn cannot authenticate it returns
// ErrUnauthorized so the caller can print a `nui login` nudge instead of a
// stack trace.
func (c *Client) WhoAmI(ctx context.Context) (Identity, error) {
	out, err := c.run(ctx, "whoami", "--json")
	if err != nil {
		return Identity{}, err
	}

	var raw rawWhoAmI
	if derr := Decode(out, &raw); derr != nil {
		return Identity{}, derr
	}

	// A logged-out ntn may exit zero but return an empty/identity-less payload;
	// treat the absence of any workspace or id as unauthorized rather than
	// reporting a blank identity.
	if strings.TrimSpace(raw.ID) == "" && strings.TrimSpace(raw.Bot.WorkspaceName) == "" {
		return Identity{}, ErrUnauthorized
	}

	return Identity{
		WorkspaceName: raw.Bot.WorkspaceName,
		BotName:       raw.Name,
		UserName:      raw.Bot.Owner.User.Name,
		UserEmail:     raw.Bot.Owner.User.Person.Email,
	}, nil
}

// String renders an Identity for the `nui whoami` command output.
func (id Identity) String() string {
	var b strings.Builder
	if id.WorkspaceName != "" {
		fmt.Fprintf(&b, "workspace: %s\n", id.WorkspaceName)
	}
	if id.UserName != "" {
		who := id.UserName
		if id.UserEmail != "" {
			who += " <" + id.UserEmail + ">"
		}
		fmt.Fprintf(&b, "user:      %s\n", who)
	}
	if id.BotName != "" {
		fmt.Fprintf(&b, "via:       %s (ntn integration)\n", id.BotName)
	}
	return strings.TrimRight(b.String(), "\n")
}
