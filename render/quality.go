// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

// QualityToInt maps the Python ImageQualityEnum string values to JPEG integer
// quality levels. Returns 50 (medium) for any unrecognised value.
func QualityToInt(quality string) int {
	switch quality {
	case "lowest":
		return 5
	case "low":
		return 15
	case "medium":
		return 50
	case "high":
		return 80
	case "highest":
		return 95
	default:
		return 50
	}
}
