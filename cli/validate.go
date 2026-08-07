package cli

import (
	"fmt"
	"strings"

	"github.com/voska/vtexkit/cli/errfmt"
)

// validateID rejects agent-supplied identifiers that could alter a request
// path. Agents hallucinate IDs with query fragments and encoded characters,
// and these values are interpolated straight into URLs.
func validateID(id string) error {
	if id == "" {
		return errfmt.Usage("empty identifier")
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return errfmt.Usage(fmt.Sprintf("identifier %q contains a control character", id))
		}
	}
	if i := strings.IndexAny(id, "?#%/\\"); i >= 0 {
		return errfmt.Usage(fmt.Sprintf(
			"identifier %q contains %q, which is not valid in a SKU or order id",
			id, string(id[i])))
	}
	return nil
}
