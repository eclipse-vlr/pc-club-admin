// Package migrations contains the immutable SQL release manifest.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
