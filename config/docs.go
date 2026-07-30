// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import "github.com/zextras/carbonio-preview-ce/v3/configdocs"

// DocsFromRegistry converts the live key registry into a configdocs.Docs value
// ready for rendering.
//
// This is the single conversion from config.KeyEntry to configdocs.RawKey:
// both the generator (cmd/configdocs, which writes the committed
// docs/configs.md) and the runtime --setup path (ConfigsMd, below) go through
// it, which is why the two can never disagree.
func DocsFromRegistry() configdocs.Docs {
	keys := RegisteredKeys()
	raw := make([]configdocs.RawKey, len(keys))
	for i, k := range keys {
		raw[i] = configdocs.RawKey{
			Key:            k.Key,
			Namespace:      string(k.Namespace),
			Default:        k.Default,
			IfNotPresent:   k.IfNotPresent,
			HiddenFromDocs: k.HiddenFromDocs,
		}
	}
	return configdocs.BuildDocs(ServiceName, ShortName, raw)
}

// ConfigsMd renders the Markdown configuration reference from the live key
// registry and returns it. It is what `carbonio-preview --setup <consul-url>`
// prints (see config/migrate.RunSetup), mirroring
// SetupAwareMain.printConfigDocumentation in carbonio-quarkus-extensions.
//
// The registry is compiled into the binary, so this needs no embedded file and
// no read from disk: docs/configs.md holds build-time generated OUTPUT only and
// is never consulted at runtime. The two are byte-identical by construction
// (same registry, same DocsFromRegistry, same configdocs.RenderMd), and the
// drift guards in configdocs/render_test.go keep them that way.
func ConfigsMd() string {
	return configdocs.RenderMd(DocsFromRegistry())
}
