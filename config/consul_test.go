// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// encodeB64 is a local helper for base64-encoding strings in test data.
func encodeB64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestConsulClientRecurseAndDecode(t *testing.T) {
	// Build a consul-style KV response: mix of real entries, null value, and
	// prefix-only entry.
	const kvPrefix = "carbonio-preview/"
	entries := []kvEntry{
		// Real entries.
		{Key: kvPrefix + "timeout-in-seconds", Value: ptr(encodeB64("60"))},
		{Key: kvPrefix + "storages/download-api", Value: ptr(encodeB64("get"))},
		// Null Value — must be filtered out.
		{Key: kvPrefix + "workers", Value: nil},
		// Prefix-only entry — must be filtered out (key == prefix, no suffix).
		{Key: kvPrefix, Value: ptr(encodeB64("ignored"))},
	}

	var gotRecurse bool
	var gotToken string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kv/"+kvPrefix {
			http.NotFound(w, r)
			return
		}
		// Consul uses bare ?recurse with no value; check the raw query string.
		gotRecurse = r.URL.RawQuery == "recurse" || strings.Contains(r.URL.RawQuery, "recurse")
		gotToken = r.Header.Get("X-Consul-Token")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	t.Setenv("CONSUL_HTTP_TOKEN", "test-token")

	m, err := fetchConsulKV(host, port)
	if err != nil {
		t.Fatalf("fetchConsulKV: %v", err)
	}

	if !gotRecurse {
		t.Error("?recurse query param not sent")
	}
	if gotToken != "test-token" {
		t.Errorf("X-Consul-Token = %q, want %q", gotToken, "test-token")
	}

	// Real entries: slash→dot conversion applied.
	if m["timeout-in-seconds"] != "60" {
		t.Errorf("timeout-in-seconds = %q, want %q", m["timeout-in-seconds"], "60")
	}
	if m["storages.download-api"] != "get" {
		t.Errorf("storages.download-api = %q, want %q", m["storages.download-api"], "get")
	}

	// Null value entry must not be present.
	if _, ok := m["workers"]; ok {
		t.Error("workers (null value) must be filtered out")
	}

	// Prefix-only entry must not be present (key == prefix, empty suffix).
	if _, ok := m[""]; ok {
		t.Error("prefix-only entry must be filtered out")
	}

	// Total: only 2 real entries.
	if len(m) != 2 {
		t.Errorf("expected 2 entries, got %d: %v", len(m), m)
	}
}

func TestConsulClientNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Consul-Token") != "" {
			t.Error("token header sent when env var is empty")
		}
		json.NewEncoder(w).Encode([]kvEntry{})
	}))
	defer srv.Close()

	t.Setenv("CONSUL_HTTP_TOKEN", "")

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	_, err := fetchConsulKV(host, port)
	if err != nil {
		t.Fatalf("fetchConsulKV: %v", err)
	}
}

func TestConsulClient404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	m, err := fetchConsulKV(host, port)
	if err != nil {
		t.Fatalf("404 should return empty map, got error: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("404: expected empty map, got %v", m)
	}
}

func TestConsulClient500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	_, err := fetchConsulKV(host, port)
	if err == nil {
		t.Fatal("500: expected error, got nil")
	}
}

func TestConsulClientConnectionRefused(t *testing.T) {
	// Find a free port then close it immediately so it's not listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	host, port, _ := net.SplitHostPort(addr)
	_, err = fetchConsulKV(host, port)
	if err == nil {
		t.Fatal("connection refused: expected error, got nil")
	}
}

// ptr returns a pointer to s, used for building test KV entries.
func ptr(s string) *string { return &s }

func TestConsulClientRecurseQueryParam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.Query().Has("recurse") {
			t.Errorf("expected ?recurse in query, got %q", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode([]kvEntry{})
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	fetchConsulKV(host, port)
}

// TestConsulClientBase64DecodeError verifies that a KV entry whose Value is not
// valid base64 makes fetchConsulKV fail (fail-fast on malformed data).
func TestConsulClientBase64DecodeError(t *testing.T) {
	const kvPrefix = "carbonio-preview/"
	bad := "not!valid!base64!" // '!' is not in the standard base64 alphabet
	entries := []kvEntry{
		{Key: kvPrefix + "broken-key", Value: &bad},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	_, err := fetchConsulKV(host, port)
	if err == nil {
		t.Fatal("expected base64 decode error, got nil")
	}
	if !strings.Contains(err.Error(), "base64 decode") {
		t.Errorf("error = %v, want it to mention base64 decode", err)
	}
}

// TestConsulClientBlankDecodedValueSkipped verifies that a KV entry whose
// decoded value is the empty string is treated as absent (extensions parity:
// blank = absent) and filtered out of the returned map.
func TestConsulClientBlankDecodedValueSkipped(t *testing.T) {
	const kvPrefix = "carbonio-preview/"
	entries := []kvEntry{
		// Decodes to "" — must be filtered out.
		{Key: kvPrefix + "blank-key", Value: ptr(encodeB64(""))},
		// A real entry alongside it must still appear.
		{Key: kvPrefix + "real-key", Value: ptr(encodeB64("present"))},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	m, err := fetchConsulKV(host, port)
	if err != nil {
		t.Fatalf("fetchConsulKV: %v", err)
	}
	if _, ok := m["blank-key"]; ok {
		t.Error("blank-decoded entry must be filtered out")
	}
	if m["real-key"] != "present" {
		t.Errorf("real-key = %q, want %q", m["real-key"], "present")
	}
	if len(m) != 1 {
		t.Errorf("expected 1 entry, got %d: %v", len(m), m)
	}
}

func TestConsulClientURLPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode([]kvEntry{})
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	fetchConsulKV(host, port)

	wantPath := fmt.Sprintf("/v1/kv/%s/", ServiceName)
	if gotPath != wantPath {
		t.Errorf("URL path = %q, want %q", gotPath, wantPath)
	}
}
