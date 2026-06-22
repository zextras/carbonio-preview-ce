// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package render

import "testing"

// TestConvertRequestedSize verifies the three-rule semantics from the Python
// service's _convert_requested_size_to_true_res_to_scale function.
func TestConvertRequestedSize(t *testing.T) {
	const minRes = 80

	tests := []struct {
		name         string
		reqX, reqY   int
		origW, origH int
		wantX, wantY int
	}{
		// Rule 1: 0x0 → original dimensions (the primary bug scenario).
		{
			name: "0x0 returns original dims",
			reqX: 0, reqY: 0,
			origW: 320, origH: 240,
			wantX: 320, wantY: 240,
		},
		// Rule 1: one axis zero.
		{
			name: "0 width returns origW, height kept",
			reqX: 0, reqY: 100,
			origW: 320, origH: 240,
			wantX: 320, wantY: 100,
		},
		{
			name: "0 height returns origH, width kept",
			reqX: 200, reqY: 0,
			origW: 320, origH: 240,
			wantX: 200, wantY: 240,
		},
		// Rule 2: sub-minimum requested dimensions → clamp to minRes.
		{
			name: "sub-min both axes",
			reqX: 10, reqY: 5,
			origW: 500, origH: 500,
			wantX: minRes, wantY: minRes,
		},
		{
			name: "sub-min width only",
			reqX: 1, reqY: 200,
			origW: 500, origH: 500,
			wantX: minRes, wantY: 200,
		},
		// Rule 1 + Rule 2 interaction: 0 → origW, then origW is sub-min.
		{
			name: "0x0 with tiny original → minRes",
			reqX: 0, reqY: 0,
			origW: 50, origH: 50,
			wantX: minRes, wantY: minRes,
		},
		// Rule 3: origW < minRes AND reqX/2 > origW → clamp reqX to minRes.
		// origW=30 (< 80), reqX=200; 200/2=100 > 30 → clamp to 80.
		{
			name: "tiny original, large requested → rule-3 clamp",
			reqX: 200, reqY: 200,
			origW: 30, origH: 30,
			wantX: minRes, wantY: minRes,
		},
		// Rule 3 negative: origW < minRes but reqX/2 <= origW → no rule-3 clamp.
		// origW=30, reqX=50; 50/2=25 <= 30 → keep reqX at minRes (rule 2 already clamped).
		{
			name: "tiny original, reqX/2 <= origW → no rule-3 clamp",
			reqX: 50, reqY: 50,
			origW: 30, origH: 30,
			wantX: minRes, wantY: minRes, // rule 2 clamp, not rule 3
		},
		// Large image, normal requested size — no clamping.
		{
			name: "normal case no clamping",
			reqX: 200, reqY: 200,
			origW: 1920, origH: 1080,
			wantX: 200, wantY: 200,
		},
		// Exact minRes requested — no change.
		{
			name: "exactly minRes requested",
			reqX: minRes, reqY: minRes,
			origW: 1000, origH: 1000,
			wantX: minRes, wantY: minRes,
		},
		// Rule 3: origW < minRes, reqX/2 == origW (boundary, not strictly >).
		// origW=40, reqX=80; 80/2=40 which is NOT > 40 → no rule-3 clamp.
		// (reqX=80 equals minRes so rule-2 result stands.)
		{
			name: "rule-3 boundary: reqX/2 == origW → no rule-3 clamp",
			reqX: 80, reqY: 80,
			origW: 40, origH: 40,
			wantX: minRes, wantY: minRes,
		},
		// 0x0 with original already at minRes — should return minRes, not re-clamp.
		{
			name: "0x0 origW==minRes → returns minRes",
			reqX: 0, reqY: 0,
			origW: minRes, origH: minRes,
			wantX: minRes, wantY: minRes,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotX, gotY := ConvertRequestedSize(tc.reqX, tc.reqY, tc.origW, tc.origH, minRes)
			if gotX != tc.wantX || gotY != tc.wantY {
				t.Errorf("ConvertRequestedSize(%d,%d,%d,%d,%d) = (%d,%d); want (%d,%d)",
					tc.reqX, tc.reqY, tc.origW, tc.origH, minRes,
					gotX, gotY,
					tc.wantX, tc.wantY,
				)
			}
		})
	}
}
