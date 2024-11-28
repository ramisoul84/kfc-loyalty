// Package migrations embeds the SQL schema so the binary can apply it on
// startup without any files on disk.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
