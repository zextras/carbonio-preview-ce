// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package configdocs contains the shared renderer used by the config-docs
// generator (cmd/configdocs) and by the drift-guard tests.
//
// The output formats replicate exactly the Java generator in
// carbonio-quarkus-extensions (ExtensionsBootstrapProcessor.generateConfigDocumentation).
package configdocs

import (
	"sort"
	"strings"
)

// RawKey describes a single config key in namespace-agnostic terms.
// Callers pass one per registry entry; BuildDocs separates them by namespace
// and constructs the display keys.
type RawKey struct {
	// Key is the dot-notation key (e.g. "storages.download-api").
	Key string

	// Namespace is "networking" or "application".
	Namespace string

	// Default is the hard-coded default; empty means absent.
	Default string

	// IfNotPresent is the note for absent-by-default keys.
	IfNotPresent string

	// HiddenFromDocs, when true, causes this key to be excluded from the
	// rendered documentation output.
	HiddenFromDocs bool
}

// BuildDocs converts a flat slice of RawKey entries (typically from
// config.RegisteredKeys()) into a Docs value ready for rendering.
//
// Keys with HiddenFromDocs=true are silently excluded from the output.
//
// Networking keys are displayed with their raw dot-notation key.
// Application keys are displayed as Consul KV paths:
//
//	serviceName + "/" + strings.ReplaceAll(key, ".", "/")
//
// This mirrors ExtensionsBootstrapProcessor.addConfigEntries display-key logic.
func BuildDocs(serviceName, shortName string, keys []RawKey) Docs {
	d := Docs{
		ServiceName: serviceName,
		ShortName:   shortName,
	}
	for _, k := range keys {
		if k.HiddenFromDocs {
			continue
		}
		e := Entry{
			Default:      k.Default,
			IfNotPresent: k.IfNotPresent,
		}
		switch k.Namespace {
		case "networking":
			e.DisplayKey = k.Key
			d.NetworkingEntries = append(d.NetworkingEntries, e)
		default: // application
			e.DisplayKey = serviceName + "/" + strings.ReplaceAll(k.Key, ".", "/")
			d.ApplicationEntries = append(d.ApplicationEntries, e)
		}
	}
	return d
}

// Entry is a single configuration key for documentation purposes.
type Entry struct {
	// DisplayKey is the key as it should appear in the documentation.
	// For networking entries this is the raw dot-notation key.
	// For application entries this is the Consul KV path
	// (e.g. "carbonio-preview/storages/download-api").
	DisplayKey string

	// Default is the hard-coded fallback value. Empty string means absent.
	Default string

	// IfNotPresent is the note shown when Default is empty and this field is
	// non-empty.  The column is omitted from the table when no entry in the
	// section has this combination.
	IfNotPresent string
}

// SectionDoc holds rendered inputs for one config section.
type SectionDoc struct {
	Entries []Entry
}

// sort returns a copy of Entries sorted alphabetically by DisplayKey.
func (s SectionDoc) sorted() []Entry {
	out := make([]Entry, len(s.Entries))
	copy(out, s.Entries)
	sort.Slice(out, func(i, j int) bool {
		return out[i].DisplayKey < out[j].DisplayKey
	})
	return out
}

// hasIfNotPresent returns true when at least one entry has an empty default
// AND a non-empty IfNotPresent (matching the Java condition).
func (s SectionDoc) hasIfNotPresent() bool {
	for _, e := range s.Entries {
		if e.Default == "" && e.IfNotPresent != "" {
			return true
		}
	}
	return false
}

// Docs groups the two sections.
type Docs struct {
	ServiceName        string // e.g. "carbonio-preview"
	ShortName          string // e.g. "preview"
	NetworkingEntries  []Entry
	ApplicationEntries []Entry
}

// RenderTxt produces the plain-text Unicode-box-table format, matching
// buildPlainText / appendUnicodeTable in ExtensionsBootstrapProcessor.
func RenderTxt(d Docs) string {
	net := SectionDoc{Entries: d.NetworkingEntries}
	app := SectionDoc{Entries: d.ApplicationEntries}

	configPropertiesPath := "/etc/carbonio/" + d.ShortName + "/config.properties"

	var sb strings.Builder
	sb.WriteString("Default Configuration\n\n")

	if len(net.Entries) > 0 {
		sb.WriteString("Networking Config (overridable by ")
		sb.WriteString(configPropertiesPath)
		sb.WriteString(")\n\n")
		appendUnicodeTable(&sb, net)
	}

	if len(app.Entries) > 0 {
		sb.WriteString("Application Config (overridable by Consul KV)\n\n")
		appendUnicodeTable(&sb, app)
	}

	return sb.String()
}

// RenderMd produces the Markdown pipe-table format, matching
// generateConfigDocumentation / appendTable in ExtensionsBootstrapProcessor.
// The output begins with an SPDX HTML comment block (year 2026, Zextras) so
// that the committed docs/configs.md satisfies REUSE/SPDX requirements.
func RenderMd(d Docs) string {
	net := SectionDoc{Entries: d.NetworkingEntries}
	app := SectionDoc{Entries: d.ApplicationEntries}

	configPropertiesPath := "/etc/carbonio/" + d.ShortName + "/config.properties"

	// spdxBlock is broken across multiple concatenations so that the reuse
	// scanner does not mistake these string literals for SPDX file headers.
	const (
		spdxCopyright = "SPDX-FileCopyright" + "Text: 2026 Zextras <https://www.zextras.com>"
		spdxLicense   = "SPDX-License-Identi" + "fier: AGPL-3.0-only"
	)

	var sb strings.Builder
	sb.WriteString("<!--\n")
	sb.WriteString(spdxCopyright)
	sb.WriteString("\n\n")
	sb.WriteString(spdxLicense)
	sb.WriteString("\n")
	sb.WriteString("-->\n")
	sb.WriteString("\n")
	sb.WriteString("# Default Configuration\n\n")

	if len(net.Entries) > 0 {
		sb.WriteString("## Networking Config\n\n")
		sb.WriteString("Overridable by `")
		sb.WriteString(configPropertiesPath)
		sb.WriteString("`\n\n")
		appendMdTable(&sb, net)
	}

	if len(app.Entries) > 0 {
		sb.WriteString("## Application Config\n\n")
		sb.WriteString("Overridable by Consul KV\n\n")
		appendMdTable(&sb, app)
	}

	return sb.String()
}

// appendUnicodeTable renders a single Unicode-box section, identical to the
// Java appendUnicodeTable method.
func appendUnicodeTable(sb *strings.Builder, sec SectionDoc) {
	entries := sec.sorted()
	showIfN := sec.hasIfNotPresent()

	// Compute column widths starting from header lengths.
	keyWidth := len("Key")
	defWidth := len("Default")
	ifnWidth := 0
	if showIfN {
		ifnWidth = len("If not set")
	}

	for _, e := range entries {
		if len(e.DisplayKey) > keyWidth {
			keyWidth = len(e.DisplayKey)
		}
		defVal := e.Default
		if defVal == "" {
			defVal = "(not set)"
		}
		if len(defVal) > defWidth {
			defWidth = len(defVal)
		}
		if showIfN && e.Default == "" {
			if len(e.IfNotPresent) > ifnWidth {
				ifnWidth = len(e.IfNotPresent)
			}
		}
	}

	// Add padding (+2 for one leading space + one trailing space).
	keyWidth += 2
	defWidth += 2
	if showIfN {
		ifnWidth += 2
	}

	keyBar := strings.Repeat("─", keyWidth)
	defBar := strings.Repeat("─", defWidth)
	ifnBar := ""
	if showIfN {
		ifnBar = strings.Repeat("─", ifnWidth)
	}

	// Top border.
	sb.WriteString("┌")
	sb.WriteString(keyBar)
	sb.WriteString("┬")
	sb.WriteString(defBar)
	if showIfN {
		sb.WriteString("┬")
		sb.WriteString(ifnBar)
	}
	sb.WriteString("┐\n")

	// Header row.
	sb.WriteString("│")
	sb.WriteString(padCell("Key", keyWidth))
	sb.WriteString("│")
	sb.WriteString(padCell("Default", defWidth))
	if showIfN {
		sb.WriteString("│")
		sb.WriteString(padCell("If not set", ifnWidth))
	}
	sb.WriteString("│\n")

	// Header separator.
	sb.WriteString("├")
	sb.WriteString(keyBar)
	sb.WriteString("┼")
	sb.WriteString(defBar)
	if showIfN {
		sb.WriteString("┼")
		sb.WriteString(ifnBar)
	}
	sb.WriteString("┤\n")

	// Data rows.
	for _, e := range entries {
		defVal := e.Default
		if defVal == "" {
			defVal = "(not set)"
		}
		sb.WriteString("│")
		sb.WriteString(padCell(e.DisplayKey, keyWidth))
		sb.WriteString("│")
		sb.WriteString(padCell(defVal, defWidth))
		if showIfN {
			ifnVal := ""
			if e.Default == "" {
				ifnVal = e.IfNotPresent
			}
			sb.WriteString("│")
			sb.WriteString(padCell(ifnVal, ifnWidth))
		}
		sb.WriteString("│\n")
	}

	// Bottom border.
	sb.WriteString("└")
	sb.WriteString(keyBar)
	sb.WriteString("┴")
	sb.WriteString(defBar)
	if showIfN {
		sb.WriteString("┴")
		sb.WriteString(ifnBar)
	}
	sb.WriteString("┘\n\n")
}

// appendMdTable renders a single Markdown pipe-table section, identical to
// the Java appendTable method.
func appendMdTable(sb *strings.Builder, sec SectionDoc) {
	entries := sec.sorted()
	showIfN := sec.hasIfNotPresent()

	if showIfN {
		sb.WriteString("| Key | Default | If not set |\n")
		sb.WriteString("| --- | ------- | ---------- |\n")
	} else {
		sb.WriteString("| Key | Default |\n")
		sb.WriteString("| --- | ------- |\n")
	}

	for _, e := range entries {
		defaultCol := "*(not set)*"
		if e.Default != "" {
			defaultCol = "`" + e.Default + "`"
		}
		sb.WriteString("| `")
		sb.WriteString(e.DisplayKey)
		sb.WriteString("` | ")
		sb.WriteString(defaultCol)
		if showIfN {
			ifNotSetCol := ""
			if e.Default == "" {
				ifNotSetCol = e.IfNotPresent
			}
			sb.WriteString(" | ")
			sb.WriteString(ifNotSetCol)
		}
		sb.WriteString(" |\n")
	}

	sb.WriteString("\n")
}

// padCell pads value to exactly width runes, with one leading space and
// trailing spaces to fill. Matches the Java pad() method exactly:
//
//	if (value.length() >= width) return " " + value + " ";
//	return " " + value + " ".repeat(width - value.length() - 1);
//
// Width is measured with len(), which counts bytes, not Unicode code points —
// matching Java's String.length() semantics. This is correct only for ASCII
// input. Registry keys, defaults, and IfNotPresent strings are ASCII by
// construction, so the output is well-defined.
func padCell(value string, width int) string {
	vlen := len(value)
	if vlen >= width {
		return " " + value + " "
	}
	trailing := width - vlen - 1
	return " " + value + strings.Repeat(" ", trailing)
}
