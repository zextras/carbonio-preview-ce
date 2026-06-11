// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

import (
	"fmt"
	"io"
	"net/http"
	"strings"
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
		client:  &http.Client{},
	}
}

// Set implements ConfigStore.  PUT /v1/kv/<key> with the value as the raw body.
func (c *consulKvStore) Set(key, value string) error {
	url := c.baseURL + "/v1/kv/" + key
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(value))
	if err != nil {
		return fmt.Errorf("consul: build PUT %s: %w", key, err)
	}
	req.Header.Set("X-Consul-Token", c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("consul: PUT %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("consul: PUT /v1/kv/%s returned HTTP %d", key, resp.StatusCode)
	}
	return nil
}

// getRaw performs GET /v1/kv/<key>?raw.
// Returns ("", nil) when the key does not exist (HTTP 404).
// Returns an error for any other non-200 status.
func (c *consulKvStore) getRaw(key string) (string, error) {
	url := c.baseURL + "/v1/kv/" + key + "?raw"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("consul: build GET %s: %w", key, err)
	}
	req.Header.Set("X-Consul-Token", c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("consul: GET %s: %w", key, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("consul: GET /v1/kv/%s returned HTTP %d", key, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("consul: read GET %s body: %w", key, err)
	}
	return string(body), nil
}

// delete performs DELETE /v1/kv/<key>.
func (c *consulKvStore) delete(key string) error {
	url := c.baseURL + "/v1/kv/" + key
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("consul: build DELETE %s: %w", key, err)
	}
	req.Header.Set("X-Consul-Token", c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("consul: DELETE %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("consul: DELETE /v1/kv/%s returned HTTP %d", key, resp.StatusCode)
	}
	return nil
}
