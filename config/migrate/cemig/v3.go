// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cemig

import "github.com/zextras/carbonio-preview-ce/v4/config/migrate"

func init() {
	if err := migrate.RegisterInSet("ce", V3RenameAsyncKeys()); err != nil {
		panic("cemig: register V3 into set \"ce\": " + err.Error())
	}
}

// V3RenameAsyncKeys returns the CE V3 migration: the four video worker keys
// move to the "async" namespace because the worker now serves every
// asynchronous thumbnail job. Verbatim value carry-overs, no Transform.
func V3RenameAsyncKeys() migrate.Migration {
	return migrate.Migration{
		Version: 3,
		Name:    "V3__RenameAsyncKeys",
		ApplicationKVMoves: []migrate.KVMove{
			{OldPath: "carbonio-preview/video/max-concurrent-extractions", NewPath: "carbonio-preview/async/max-concurrent-jobs"},
			{OldPath: "carbonio-preview/video/max-attempts", NewPath: "carbonio-preview/async/max-attempts"},
			{OldPath: "carbonio-preview/video/poll-interval-seconds", NewPath: "carbonio-preview/async/poll-interval-seconds"},
			{OldPath: "carbonio-preview/video/stuck-generation-timeout-seconds", NewPath: "carbonio-preview/async/stuck-job-timeout-seconds"},
		},
	}
}
