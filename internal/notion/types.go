package notion

// This file holds notion-internal wire shapes that are NOT part of the pure
// Document IR. The page/block raw DTOs (RawPage/RawBlock) deliberately do NOT
// live here — they belong in internal/doc/raw.go (G1) so that internal/doc
// stays import-pure and notion can depend on doc, never the reverse
// (SPEC §4 dependency rule).

// rawWhoAmI mirrors the JSON returned by `ntn whoami --json`, which is the raw
// Notion /v1/users/me response for the authenticated bot. Only the fields nui
// needs to display an identity are modeled; everything else is ignored.
type rawWhoAmI struct {
	ID     string `json:"id"`
	Name   string `json:"name"`   // the integration/bot name, e.g. "Notion CLI"
	Object string `json:"object"` // "user"
	Type   string `json:"type"`   // "bot" for an integration token
	Bot    struct {
		WorkspaceID   string `json:"workspace_id"`
		WorkspaceName string `json:"workspace_name"`
		Owner         struct {
			Type string `json:"type"`
			User struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Person struct {
					Email string `json:"email"`
				} `json:"person"`
			} `json:"user"`
		} `json:"owner"`
	} `json:"bot"`
}

// Identity is the resolved, display-ready result of WhoAmI: who nui is talking
// to Notion as, and on whose behalf.
type Identity struct {
	WorkspaceName string // "Equilibrium Energy"
	BotName       string // "Notion CLI"
	UserName      string // owner's human name, when the token is owned by a person
	UserEmail     string // owner's email, when present
}
