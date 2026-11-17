// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package migrations embeds the goose SQL migrations.
package migrations

import "embed"

// FS holds every *.sql migration file.
//
//go:embed *.sql
var FS embed.FS
