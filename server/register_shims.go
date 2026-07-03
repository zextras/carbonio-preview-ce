// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

// register_shims.go exposes the per-group registration helpers that the
// server tests call directly (same-package).  They existed as unexported
// functions in the old monolithic api.go; now the real registration lives in
// apispec but the server tests still call the old signatures.

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/zextras/carbonio-preview-ce/v3/server/apispec"
)

func registerImageOps(api huma.API, deps Deps) {
	semMW := semaphoreMiddleware(api, deps.Sem)
	apispec.RegisterImageOps(api,
		buildGetImagePreview(deps), buildGetImageThumbnail(deps),
		buildPostImagePreview(deps), buildPostImageThumbnail(deps),
		semMW)
}

func registerHealthOps(api huma.API, deps Deps) {
	apispec.RegisterHealthOps(api,
		buildGetHealthLive(), buildGetHealthReady(deps), buildGetHealth(deps))
}

func registerPDFOps(api huma.API, deps Deps) {
	semMW := semaphoreMiddleware(api, deps.Sem)
	apispec.RegisterPDFOps(api,
		buildGetPDFPreview(deps), buildGetPDFThumbnail(deps),
		buildPostPDFPreview(deps), buildPostPDFThumbnail(deps),
		semMW)
}

func registerDocumentOps(api huma.API, deps Deps) {
	semMW := semaphoreMiddleware(api, deps.Sem)
	apispec.RegisterDocumentOps(api,
		buildGetDocumentPreview(deps), buildGetDocumentThumbnail(deps),
		buildPostDocumentPreview(deps), buildPostDocumentThumbnail(deps),
		semMW)
}
