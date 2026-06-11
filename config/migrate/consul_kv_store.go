// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// consulKvStore is a ConfigStore backed by the Consul KV HTTP API.
// KV paths are raw slash paths, e.g. "carbonio-preview/timeout-in-seconds".
//
// All requests carry the X-Consul-Token header.
// Non-200 responses are returned as errors.
type consulKvStore struct {
	baseURL string
	token   string
	client  *http.Client
}

// newConsulKvStore creates a store pointing at consulBaseURL (e.g. "http://127.0.0.1:8500").
func newConsulKvStore(consulBaseURL, token string) *consulKvStore {
	return &consulKvStore{
		baseURL: strings.TrimRight(consulBaseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Set implements ConfigStore.  PUT /v1/kv/<key> with the value as the raw body.
func (c *consulKvStore) Set(key, value string) error {
	url := c.baseURL + "/v1/kv/" + key
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(value))
	if err != nil {
		return fmt.Errorf("consul: build PUT %s/v1/kv/%s: %w", c.baseURL, key, err)
	}
	req.Header.Set("X-Consul-Token", c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("consul: PUT %s/v1/kv/%s: %w", c.baseURL, key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("consul: PUT %s/v1/kv/%s returned HTTP %d", c.baseURL, key, resp.StatusCode)
	}
	return nil
}
