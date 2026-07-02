// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"net/http"
	"time"

	"github.com/zextras/carbonio-preview-ce/server/apispec"
)

// isDependencyUp performs a GET to url with a 5-second timeout.
// Returns true if the response is HTTP 2xx.
func isDependencyUp(url string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// healthDependency and healthResponse are package-local aliases for the
// apispec types so that existing same-package tests can continue to use the
// unexported names without modification.
type healthDependency = apispec.HealthDependency
type healthResponse = apispec.HealthResponse
