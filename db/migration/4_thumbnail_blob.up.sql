-- SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
--
-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE thumbnail_blob (
    blob_id      VARCHAR(64)  NOT NULL,
    owner_id     VARCHAR(64)  NOT NULL,
    service_type VARCHAR(16)  NOT NULL,
    version      INTEGER      NOT NULL,
    status       VARCHAR(16)  NOT NULL,
    attempts     INTEGER      NOT NULL DEFAULT 0,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    CONSTRAINT pk_thumbnail_blob PRIMARY KEY (blob_id),
    CONSTRAINT ck_thumbnail_blob_status CHECK (status IN ('PENDING', 'TOMBSTONE'))
);
CREATE INDEX idx_thumbnail_blob_purge ON thumbnail_blob (status, created_at);
