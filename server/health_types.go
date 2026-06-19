// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"net/http"
	"time"
)

// healthDependency is the JSON object for a single dependency in /health/.
// Shape mirrors the old FastAPI implementation (health.py:50-66).
type healthDependency struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
	Live  bool   `json:"live"`
	Type  string `json:"type"`
}

// healthResponse is the JSON body for GET /health/.
type healthResponse struct {
	Ready        bool               `json:"ready"`
	Dependencies []healthDependency `json:"dependencies"`
}

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
