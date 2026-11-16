-- SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
--
-- SPDX-License-Identifier: AGPL-3.0-only

DELETE FROM video_preview WHERE status <> 'READY' OR preview_id IS NULL;
ALTER TABLE video_preview RENAME TO thumbnail;
ALTER TABLE thumbnail RENAME COLUMN preview_id TO blob_id;
ALTER INDEX idx_video_preview_sweep RENAME TO idx_thumbnail_sweep;
ALTER INDEX idx_video_preview_pending RENAME TO idx_thumbnail_pending;
ALTER INDEX idx_video_preview_cleanup RENAME TO idx_thumbnail_cleanup;
ALTER TABLE thumbnail ADD COLUMN size VARCHAR(1) NOT NULL DEFAULT 'm';
ALTER TABLE thumbnail ALTER COLUMN size DROP DEFAULT;
ALTER TABLE thumbnail ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT 'video';
ALTER TABLE thumbnail ALTER COLUMN kind DROP DEFAULT;
ALTER TABLE thumbnail ADD COLUMN digest VARCHAR(128);
ALTER TABLE thumbnail ADD COLUMN lang VARCHAR(35);
ALTER TABLE thumbnail ADD COLUMN original BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE thumbnail ADD COLUMN content_type VARCHAR(64);
UPDATE thumbnail SET status = 'LEGACY';
ALTER TABLE thumbnail DROP CONSTRAINT pk_video_preview;
ALTER TABLE thumbnail ADD CONSTRAINT pk_thumbnail PRIMARY KEY (file_id, version, size);
