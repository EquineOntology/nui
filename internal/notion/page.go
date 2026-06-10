package notion

import (
	"context"
	"fmt"
	"net/url"

	"github.com/EQuineOntology/nui/internal/doc"
)

// Page fetches a page's metadata — title, icon, cover, properties — from
// /v1/pages/{id} (SPEC §7). It does NOT fetch body blocks; those come from
// Blocks. The response is decoded tolerantly (control-char sanitization) into
// the pure doc.RawPage DTO, which doc.Build later maps into the Document header.
//
// Databases/data_sources are not pages and will 404 here; the caller falls back
// to a data-source query (the bash _fetch_raw pattern). G1 models only
// /v1/pages + /v1/blocks; data-source rendering lands with views in G4.
func (c *Client) Page(ctx context.Context, id string) (doc.RawPage, error) {
	var p doc.RawPage
	path := fmt.Sprintf("/v1/pages/%s", url.PathEscape(id))
	if err := c.API(ctx, path, "", &p); err != nil {
		return doc.RawPage{}, err
	}
	return p, nil
}
