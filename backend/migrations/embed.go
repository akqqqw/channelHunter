// Package migrations is the single source of truth for the database schema:
// it holds the golang-migrate SQL files and embeds them into the binaries so
// containers and `go test` never depend on the working directory.
package migrations

import (
	"embed"
)

// FS contains every *.sql migration file.
//
//go:embed *.sql
var FS embed.FS

// DirName is the directory inside the filesystem that holds the migrations.
const DirName = "."
