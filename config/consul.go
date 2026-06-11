package config

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// kvEntry mirrors one object in the JSON array returned by the Consul KV API.
type kvEntry struct {
	// Key is the full KV path, e.g. "carbonio-preview/storages/download-api".
	Key string `json:"Key"`

	// Value is the base64-encoded value, or nil when the key has no value.
	Value *string `json:"Value"`
}

// fetchConsulKV performs a recursive GET against the Consul KV API at
// http://{host}:{port}/v1/kv/carbonio-preview/?recurse and returns a
// map of dot-notation key → decoded string value.
//
// Behaviour:
//   - HTTP 404 → empty map, nil error (prefix not yet populated).
//   - Any other non-200 status or connection error → non-nil error (fail-fast).
//   - Entries with a nil Value are filtered out.
//   - The prefix-only entry (key == "carbonio-preview/") is filtered out.
//   - KV path suffixes are converted back to dot-notation by replacing '/' with '.'.
//
// The CONSUL_HTTP_TOKEN environment variable is sent as the X-Consul-Token
// header when non-empty.
//
// Timeouts: dial/connection 2 s, whole request 5 s.
func fetchConsulKV(host, port string) (map[string]string, error) {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 2 * time.Second,
		}).DialContext,
	}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
	}

	kvPrefix := ServiceName + "/"
	url := fmt.Sprintf("http://%s:%s/v1/kv/%s", host, port, kvPrefix)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("consul: build request: %w", err)
	}
	req.URL.RawQuery = "recurse"

	if token := os.Getenv("CONSUL_HTTP_TOKEN"); token != "" {
		req.Header.Set("X-Consul-Token", token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("consul: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return map[string]string{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("consul: unexpected status %d", resp.StatusCode)
	}

	var entries []kvEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("consul: decode response: %w", err)
	}

	result := make(map[string]string, len(entries))
	for _, e := range entries {
		// Must have a non-nil value.
		if e.Value == nil {
			continue
		}
		// Must be longer than the prefix (not the prefix-only entry).
		if len(e.Key) <= len(kvPrefix) {
			continue
		}
		suffix := e.Key[len(kvPrefix):]
		if suffix == "" {
			continue
		}

		decoded, err := base64.StdEncoding.DecodeString(*e.Value)
		if err != nil {
			return nil, fmt.Errorf("consul: base64 decode key %q: %w", e.Key, err)
		}

		// Blank decoded values are treated as absent (extensions parity: an
		// empty KV entry falls through to the next layer, just like an empty
		// Optional in SmallRye).
		if string(decoded) == "" {
			continue
		}

		// Convert the KV path suffix back to dot-notation.
		dotKey := strings.ReplaceAll(suffix, "/", ".")
		result[dotKey] = string(decoded)
	}

	return result, nil
}
