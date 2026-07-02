// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// ── Level parsing ─────────────────────────────────────────────────────────────

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input     string
		wantLevel slog.Level
		wantErr   bool
	}{
		// canonical values
		{"debug", slog.LevelDebug, false},
		{"info", slog.LevelInfo, false},
		{"warning", slog.LevelWarn, false},
		{"warn", slog.LevelWarn, false}, // Python alias
		{"error", slog.LevelError, false},
		{"critical", slog.LevelError, false}, // mapped to Error; no slog equivalent
		// case-insensitive
		{"DEBUG", slog.LevelDebug, false},
		{"INFO", slog.LevelInfo, false},
		{"WARNING", slog.LevelWarn, false},
		{"WARN", slog.LevelWarn, false},
		{"ERROR", slog.LevelError, false},
		{"CRITICAL", slog.LevelError, false},
		{"Warning", slog.LevelWarn, false},
		// absent / empty → info default
		{"", slog.LevelInfo, false},
		// invalid → error
		{"verbose", slog.LevelInfo, true},
		{"TRACE", slog.LevelInfo, true},
		{"42", slog.LevelInfo, true},
	}

	for _, tt := range tests {
		t.Run(tt.input+"_input", func(t *testing.T) {
			got, err := parseLogLevel(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseLogLevel(%q): expected error, got nil", tt.input)
				} else {
					// Error message must mention the env var name and the bad value.
					if !strings.Contains(err.Error(), "PREVIEW_LOG_LEVEL") {
						t.Errorf("error %q should mention PREVIEW_LOG_LEVEL", err.Error())
					}
					if tt.input != "" && !strings.Contains(err.Error(), tt.input) {
						t.Errorf("error %q should mention the invalid value %q", err.Error(), tt.input)
					}
				}
				return
			}
			if err != nil {
				t.Errorf("parseLogLevel(%q): unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.wantLevel {
				t.Errorf("parseLogLevel(%q) = %v, want %v", tt.input, got, tt.wantLevel)
			}
		})
	}
}

// TestLoadLogLevel_AbsentEnv verifies that a missing PREVIEW_LOG_LEVEL → Info.
func TestLoadLogLevel_AbsentEnv(t *testing.T) {
	t.Setenv("PREVIEW_LOG_LEVEL", "")
	level, err := loadLogLevel()
	if err != nil {
		t.Fatalf("loadLogLevel with absent env: unexpected error: %v", err)
	}
	if level != slog.LevelInfo {
		t.Errorf("loadLogLevel absent → %v, want Info", level)
	}
}

// TestLoadLogLevel_ValidEnv verifies that a valid PREVIEW_LOG_LEVEL is honoured.
func TestLoadLogLevel_ValidEnv(t *testing.T) {
	t.Setenv("PREVIEW_LOG_LEVEL", "debug")
	level, err := loadLogLevel()
	if err != nil {
		t.Fatalf("loadLogLevel with debug: unexpected error: %v", err)
	}
	if level != slog.LevelDebug {
		t.Errorf("loadLogLevel debug → %v, want Debug", level)
	}
}

// TestLoadLogLevel_InvalidEnv verifies that an invalid PREVIEW_LOG_LEVEL → error.
func TestLoadLogLevel_InvalidEnv(t *testing.T) {
	t.Setenv("PREVIEW_LOG_LEVEL", "banana")
	_, err := loadLogLevel()
	if err == nil {
		t.Fatal("loadLogLevel with invalid value: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "PREVIEW_LOG_LEVEL") {
		t.Errorf("error should mention PREVIEW_LOG_LEVEL: %v", err)
	}
	if !strings.Contains(err.Error(), "banana") {
		t.Errorf("error should mention the bad value 'banana': %v", err)
	}
}

// ── Level filtering ───────────────────────────────────────────────────────────

// TestLevelFiltering_DebugSuppressedAtInfo verifies that Debug records are
// suppressed when the effective level is Info.
func TestLevelFiltering_DebugSuppressedAtInfo(t *testing.T) {
	var buf bytes.Buffer
	lv := &slog.LevelVar{}
	lv.Set(slog.LevelInfo)
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: lv})
	logger := slog.New(h)

	logger.Debug("this should not appear")
	logger.Info("this should appear")

	out := buf.String()
	if strings.Contains(out, "should not appear") {
		t.Error("Debug message was logged at Info level — should have been suppressed")
	}
	if !strings.Contains(out, "should appear") {
		t.Error("Info message was not logged at Info level")
	}
}

// TestLevelFiltering_DebugEmittedAtDebug verifies that Debug records appear
// when the effective level is Debug.
func TestLevelFiltering_DebugEmittedAtDebug(t *testing.T) {
	var buf bytes.Buffer
	lv := &slog.LevelVar{}
	lv.Set(slog.LevelDebug)
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: lv})
	logger := slog.New(h)

	logger.Debug("debug message here")

	if !strings.Contains(buf.String(), "debug message here") {
		t.Error("Debug message not logged at Debug level")
	}
}
