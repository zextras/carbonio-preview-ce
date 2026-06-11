// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package migrate

// ConfigStore is the write interface exposed to migration entry functions.
// Both the networking properties store and the Consul KV store implement it.
type ConfigStore interface {
	Set(key, value string) error
}
