-- SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
--
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE video_preview ADD COLUMN IF NOT EXISTS last_accessed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS idx_video_preview_cleanup ON video_preview (status, last_accessed_at);
DELETE FROM video_preview WHERE status = 'FAILED';
