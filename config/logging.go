// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// logLevelVar is the package-level dynamic level used by the default slog handler.
// It defaults to Info so that any log emitted before Load() runs is visible.
var logLevelVar = &slog.LevelVar{} // zero value = Info

func init() {
	// Install the default slog handler at startup so every package that calls
	// slog.Info/Debug/… before Load() uses the TextHandler on stderr.
	installHandler()
}

// installHandler sets slog.Default to a TextHandler on stderr driven by logLevelVar.
func installHandler() {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevelVar})
	slog.SetDefault(slog.New(h))
}

// parseLogLevel converts the PREVIEW_LOG_LEVEL string (case-insensitive) to an
// slog.Level.  Python-logging parity:
//
//	debug    → slog.LevelDebug  (-4)
//	info     → slog.LevelInfo   (0)
//	warning  → slog.LevelWarn   (4)
//	warn     → slog.LevelWarn   (4)   — alias accepted by Python logging
//	error    → slog.LevelError  (8)
//	critical → slog.LevelError  (8)   — Python CRITICAL has no slog equivalent; map to Error
//
// An empty string returns (Info, nil) — absent env var → info default.
// Any other value returns an error naming the env var and the rejected value.
func parseLogLevel(raw string) (slog.Level, error) {
	if raw == "" {
		return slog.LevelInfo, nil
	}
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warning", "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	case "critical":
		// Python CRITICAL (50) has no slog equivalent; map to slog.LevelError (8).
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf(
			"config: PREVIEW_LOG_LEVEL has invalid value %q; accepted: debug, info, warning, warn, error, critical",
			raw,
		)
	}
}

// loadLogLevel reads PREVIEW_LOG_LEVEL directly from the OS environment (outside
// the extensions config chain — this is a per-instance, framework-level knob
// equivalent to QUARKUS_LOG_LEVEL in Quarkus services).
//
// It returns (Info, nil) when the variable is absent or empty.
// It returns an error on an unrecognised value so Load() can fail-fast.
func loadLogLevel() (slog.Level, error) {
	return parseLogLevel(os.Getenv("PREVIEW_LOG_LEVEL"))
}
