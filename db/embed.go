// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package db provides the database connection pool, migration runner, and
// all video_preview SQL operations for carbonio-preview-ce.
package db

import "embed"

// MigrationFS holds the embedded SQL migration files under db/migration/.
//
//go:embed migration/*.sql
var MigrationFS embed.FS
