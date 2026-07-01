-- SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
--
-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE IF NOT EXISTS video_preview (
    file_id      VARCHAR(64)  NOT NULL,
    version      INTEGER      NOT NULL DEFAULT 0,
    status       VARCHAR(32)  NOT NULL,
    preview_id   VARCHAR(64),
    owner_id     VARCHAR(64)  NOT NULL,
    service_type VARCHAR(16)  NOT NULL,
    claimed_by   VARCHAR(64),
    claimed_at   TIMESTAMP WITH TIME ZONE,
    attempts     INT          NOT NULL DEFAULT 0,
    codec        VARCHAR(64),
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL,
    CONSTRAINT pk_video_preview PRIMARY KEY (file_id, version)
);
CREATE INDEX IF NOT EXISTS idx_video_preview_sweep   ON video_preview (status, claimed_at);
CREATE INDEX IF NOT EXISTS idx_video_preview_pending ON video_preview (status, created_at);
