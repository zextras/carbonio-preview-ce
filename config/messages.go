// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"os"

	"gopkg.in/ini.v1"
)

// Messages holds every hard-coded error string produced by the service.
// Values match messages.ini exactly so that clients (and existing tests)
// see byte-identical bodies.
type Messages struct {
	// [hard_errors]
	StorageUnavailable    string
	GenericErrorStorage   string
	ItemNotFound          string
	InputError            string
	DocsEditorUnavailable string

	// [validation]
	HeightOrWidthNotInserted  string
	NumberOfPagesNotValid     string
	HeightWidthNotValid       string
	IDNotValid                string
	VersionNotValid           string
	FormatNotSupported        string
	FileNotValid              string
	DocumentThumbnailDisabled string
	DocumentPreviewDisabled   string
}

// Msg is the package-level, process-wide messages instance.
// Populated by Load() alongside App.
var Msg Messages

// messagesSearchPaths returns candidate locations for messages.ini.
var messagesSearchPaths = []string{
	"/etc/carbonio/preview/messages.ini",
}

// hardcodedMessages returns the canonical defaults (exact strings from
// the Python messages.ini).
func hardcodedMessages() Messages {
	return Messages{
		StorageUnavailable:    "Storage is currently unavailable.",
		GenericErrorStorage:   "Storage is available but there was an error executing your request.",
		ItemNotFound:          "Requested item was not found in the storage.",
		InputError:            "Some values in the query were not correct.",
		DocsEditorUnavailable: "Carbonio-docs-editor is currently unavailable, document preview service is currently offline.",

		HeightOrWidthNotInserted:  "Height or width not found, example of valid input: 120x250.",
		NumberOfPagesNotValid:     "Pages must be at least 1.",
		HeightWidthNotValid:       "Height or width values must be integers >= 0.",
		IDNotValid:                "Id is not in a valid format, UUID1 to UUID4 are supported.",
		VersionNotValid:           "Version is not valid, the accepted values are > 0.",
		FormatNotSupported:        "Format not supported.",
		FileNotValid:              "The input file should not be null.",
		DocumentThumbnailDisabled: "The document thumbnail function is not currently enabled!",
		DocumentPreviewDisabled:   "The document preview function is not currently enabled!",
	}
}

// loadMessages reads messages.ini if it exists and returns the populated
// Messages struct; otherwise returns the hardcoded defaults.
func loadMessages() Messages {
	m := hardcodedMessages()

	for _, p := range messagesSearchPaths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		cfg, err := ini.Load(p)
		if err != nil {
			continue
		}
		overrideMessages(cfg, &m)
		break
	}
	return m
}

func msgStr(cfg *ini.File, section, key, fallback string) string {
	s, err := cfg.GetSection(section)
	if err != nil {
		return fallback
	}
	k, err := s.GetKey(key)
	if err != nil {
		return fallback
	}
	v := k.String()
	if v == "" {
		return fallback
	}
	return v
}

func overrideMessages(cfg *ini.File, m *Messages) {
	const (
		secHard = "hard_errors"
		secVal  = "validation"
	)

	// Key names match the actual keys in package/messages.ini (lowercase).
	m.StorageUnavailable = msgStr(cfg, secHard, "storage_unavailable_string", m.StorageUnavailable)
	m.GenericErrorStorage = msgStr(cfg, secHard, "generic_error_with_storage", m.GenericErrorStorage)
	m.ItemNotFound = msgStr(cfg, secHard, "item_not_found", m.ItemNotFound)
	m.InputError = msgStr(cfg, secHard, "input_error", m.InputError)
	m.DocsEditorUnavailable = msgStr(cfg, secHard, "carbonio_docs_editor_not_running", m.DocsEditorUnavailable)

	m.HeightOrWidthNotInserted = msgStr(cfg, secVal, "height_or_width_not_inserted_error", m.HeightOrWidthNotInserted)
	m.NumberOfPagesNotValid = msgStr(cfg, secVal, "number_of_pages_not_valid_error", m.NumberOfPagesNotValid)
	m.HeightWidthNotValid = msgStr(cfg, secVal, "height_or_width_not_valid_error", m.HeightWidthNotValid)
	m.IDNotValid = msgStr(cfg, secVal, "id_not_valid_error", m.IDNotValid)
	m.VersionNotValid = msgStr(cfg, secVal, "version_not_valid_error", m.VersionNotValid)
	m.FormatNotSupported = msgStr(cfg, secVal, "format_not_supported_error", m.FormatNotSupported)
	m.FileNotValid = msgStr(cfg, secVal, "file_not_valid_error", m.FileNotValid)
	m.DocumentThumbnailDisabled = msgStr(cfg, secVal, "document_thumbnail_not_enabled_error", m.DocumentThumbnailDisabled)
	m.DocumentPreviewDisabled = msgStr(cfg, secVal, "document_preview_not_enabled_error", m.DocumentPreviewDisabled)
}

// init loads messages together with the main config so that Msg is
// always populated when the package is imported. Errors are silently
// swallowed — hardcoded defaults always apply.
func init() {
	Msg = loadMessages()
}
