// Package fonts embeds the fonts the signing module draws with: DejaVu Sans
// (Latin, Cyrillic, Greek; field values, stamps and the audit trail) and
// Great Vibes (typed signatures). Both are free fonts carried over from v3:
// DejaVu under the Bitstream Vera / DejaVu licence, Great Vibes under the SIL
// Open Font License 1.1 (see LICENSES.md).
package fonts

import _ "embed"

// DejaVuSans is the regular DejaVu Sans TrueType font.
//
//go:embed DejaVuSans.ttf
var DejaVuSans []byte

// GreatVibes is the Great Vibes TrueType font (typed signatures).
//
//go:embed GreatVibes-Regular.ttf
var GreatVibes []byte
