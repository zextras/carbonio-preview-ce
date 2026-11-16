// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"io"
	"sync"

	"github.com/zextras/carbonio-preview-ce/v4/storage"
)

type memKey struct {
	Node        string
	Version     int
	ServiceType string
	Owner       string
}

type memStore struct {
	mu      sync.Mutex
	blobs   map[memKey][]byte
	gets    []memKey
	puts    []memKey
	deletes []memKey

	getErr    func(memKey) error
	putErr    func(memKey) error
	deleteErr func(memKey) error
	onGet     func(memKey)
}

func newMemStore() *memStore {
	return &memStore{blobs: map[memKey][]byte{}}
}

func (m *memStore) seed(node string, version int, serviceType, owner string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobs[memKey{node, version, serviceType, owner}] = data
}

func (m *memStore) has(node string, version int, serviceType, owner string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.blobs[memKey{node, version, serviceType, owner}]
	return ok
}

func (m *memStore) blobCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.blobs)
}

func (m *memStore) getCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.gets)
}

func (m *memStore) putKeys() []memKey {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]memKey(nil), m.puts...)
}

func (m *memStore) deletedKeys() []memKey {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]memKey(nil), m.deletes...)
}

func (m *memStore) get(node string, version int, serviceType, owner string) ([]byte, error) {
	k := memKey{node, version, serviceType, owner}
	m.mu.Lock()
	m.gets = append(m.gets, k)
	hook, errFn := m.onGet, m.getErr
	b, ok := m.blobs[k]
	m.mu.Unlock()
	if hook != nil {
		hook(k)
	}
	if errFn != nil {
		if err := errFn(k); err != nil {
			return nil, err
		}
	}
	if !ok {
		return nil, storage.ErrNotFound
	}
	return b, nil
}

func (m *memStore) RetrieveData(_ context.Context, id string, version int, serviceType, owner string) (storage.Blob, error) {
	return m.get(id, version, serviceType, owner)
}

func (m *memStore) RetrieveDataStreaming(_ context.Context, id string, version int, serviceType, owner string) (io.ReadCloser, int64, error) {
	b, err := m.get(id, version, serviceType, owner)
	if err != nil {
		return nil, 0, err
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}

func (m *memStore) StoreData(_ context.Context, id string, version int, serviceType, owner string, data []byte) (string, error) {
	k := memKey{id, version, serviceType, owner}
	m.mu.Lock()
	errFn := m.putErr
	m.mu.Unlock()
	if errFn != nil {
		if err := errFn(k); err != nil {
			return "", err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts = append(m.puts, k)
	m.blobs[k] = append([]byte(nil), data...)
	return id, nil
}

func (m *memStore) Delete(_ context.Context, id string, version int, serviceType, owner string) error {
	k := memKey{id, version, serviceType, owner}
	m.mu.Lock()
	errFn := m.deleteErr
	m.mu.Unlock()
	if errFn != nil {
		if err := errFn(k); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletes = append(m.deletes, k)
	if _, ok := m.blobs[k]; !ok {
		return storage.ErrNotFound
	}
	delete(m.blobs, k)
	return nil
}
