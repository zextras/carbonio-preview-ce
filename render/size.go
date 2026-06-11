package render

// ImageMinRes is the minimum resolution enforced by ConvertRequestedSize.
// It mirrors the Python IMAGE_MIN_RES constant (image_constants.minimum_resolution,
// default 80). Call render.SetImageMinRes(cfg.ImageMinimumResolution) once in
// main after config.Load() to pick up operator-configured values.
var ImageMinRes = 80

// SetImageMinRes sets the minimum resolution used by ConvertRequestedSize.
// It is NOT goroutine-safe; call it once at program startup, before any
// concurrent render calls.
func SetImageMinRes(min int) {
	if min > 0 {
		ImageMinRes = min
	}
}

// ConvertRequestedSize converts a requested (reqX, reqY) target dimension pair
// to safe non-zero values that libvips will accept, replicating exactly the
// Python service's _convert_requested_size_to_true_res_to_scale semantics
// (app/core/services/image_manipulation/image_manipulation.py).
//
// Rules applied in order:
//  1. If reqX == 0, use origW (0 means "keep original width").
//     If reqY == 0, use origH (0 means "keep original height").
//  2. If reqX < minRes, clamp to minRes.
//     If reqY < minRes, clamp to minRes.
//  3. If origW < minRes and reqX/2 > origW, clamp reqX to minRes.
//     If origH < minRes and reqY/2 > origH, clamp reqY to minRes.
//
// The returned values are always >= 1 (and >= minRes when minRes >= 1).
// This prevents passing width=0 or height=0 to libvips, which causes:
//
//	"value 0 of type gint is invalid for property width/height"
//
// origW/origH are the actual source image dimensions.
// minRes is the configured minimum resolution (IMAGE_MIN_RES, default 80).
func ConvertRequestedSize(reqX, reqY, origW, origH, minRes int) (int, int) {
	// Rule 1: 0 means "use original dimension".
	if reqX == 0 {
		reqX = origW
	}
	if reqY == 0 {
		reqY = origH
	}

	// Rule 2: clamp to minimum resolution.
	if reqX < minRes {
		reqX = minRes
	}
	if reqY < minRes {
		reqY = minRes
	}

	// Rule 3: if the original is smaller than the minimum AND the requested size
	// is more than double the original, we cannot upscale to reach reqX/reqY
	// without losing fidelity — cap at minRes.
	// (Python: if original_width < IMAGE_MIN_RES and requested_x / 2 > original_width)
	if origW < minRes && reqX/2 > origW {
		reqX = minRes
	}
	if origH < minRes && reqY/2 > origH {
		reqY = minRes
	}

	return reqX, reqY
}
