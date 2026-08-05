-- +goose Up

CREATE TABLE refresh_sessions (
    id              VARCHAR(64) PRIMARY KEY,
    family_id       VARCHAR(64) NOT NULL,
    user_id         BIGINT      NOT NULL,
    token_hash      BYTEA       NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    consumed_at     TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    replaced_by     VARCHAR(64),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_refresh_sessions_token_hash UNIQUE (token_hash),
    CONSTRAINT ck_refresh_sessions_token_hash_length
        CHECK (octet_length(token_hash) = 32),
    CONSTRAINT fk_refresh_sessions_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_refresh_sessions_replaced_by
        FOREIGN KEY (replaced_by) REFERENCES refresh_sessions(id) ON DELETE SET NULL
);

CREATE INDEX idx_refresh_sessions_family_id ON refresh_sessions (family_id);
CREATE INDEX idx_refresh_sessions_user_id ON refresh_sessions (user_id);
CREATE INDEX idx_refresh_sessions_expires_at ON refresh_sessions (expires_at);
CREATE INDEX idx_refresh_sessions_replaced_by ON refresh_sessions (replaced_by);

-- +goose Down

DROP TABLE IF EXISTS refresh_sessions;
