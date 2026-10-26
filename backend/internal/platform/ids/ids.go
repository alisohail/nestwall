// Package ids generates identifiers for new database rows.
//
// SQLite has no UUID generator, so the API creates IDs itself. Every ID is a
// random RFC 4122 version 4 UUID in lowercase text form.
package ids

import (
	"crypto/rand"
	"fmt"
)

// New returns a new random UUID v4 string.
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("ids: crypto/rand failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
