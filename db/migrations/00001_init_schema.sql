-- +goose Up

CREATE TABLE users (
    id              BIGSERIAL PRIMARY KEY,
    email           VARCHAR(255) NOT NULL,
    password_hash   VARCHAR(255),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_users_email ON users (email);

CREATE TABLE oauth_accounts (
    user_id         BIGINT       NOT NULL,
    provider        VARCHAR(32)  NOT NULL,
    provider_uid    VARCHAR(255) NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT oauth_accounts_pkey PRIMARY KEY (provider, provider_uid),
    CONSTRAINT fk_oauth_accounts_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_oauth_accounts_user_id ON oauth_accounts (user_id);

CREATE TABLE symbols (
    code            VARCHAR(20)  PRIMARY KEY,
    name            VARCHAR(255) NOT NULL,
    market          VARCHAR(100) NOT NULL,
    timezone        VARCHAR(64)  NOT NULL,
    logo_url        TEXT,
    logo_updated_at TIMESTAMPTZ,
    is_active       BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE candles (
    symbol_code     VARCHAR(20)    NOT NULL,
    "interval"      VARCHAR(16)    NOT NULL,
    "time"          TIMESTAMPTZ    NOT NULL,
    open            NUMERIC(15, 4) NOT NULL,
    high            NUMERIC(15, 4) NOT NULL,
    low             NUMERIC(15, 4) NOT NULL,
    close           NUMERIC(15, 4) NOT NULL,
    volume          BIGINT         NOT NULL DEFAULT 0,
    CONSTRAINT fk_candles_symbol
        FOREIGN KEY (symbol_code) REFERENCES symbols(code) ON DELETE RESTRICT,
    CONSTRAINT candles_pkey PRIMARY KEY (symbol_code, "interval", "time")
);

CREATE TABLE watchlists (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT      NOT NULL,
    symbol_code     VARCHAR(20) NOT NULL,
    sort_key        BIGINT      NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT fk_watchlists_user
        FOREIGN KEY (user_id)     REFERENCES users(id)     ON DELETE CASCADE,
    CONSTRAINT fk_watchlists_symbol
        FOREIGN KEY (symbol_code) REFERENCES symbols(code) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX idx_watchlist_user_symbol   ON watchlists (user_id, symbol_code);
CREATE UNIQUE INDEX idx_watchlist_user_sort_key ON watchlists (user_id, sort_key);
CREATE INDEX        idx_watchlists_symbol_code  ON watchlists (symbol_code);

-- +goose Down

DROP TABLE IF EXISTS watchlists;
DROP TABLE IF EXISTS candles;
DROP TABLE IF EXISTS symbols;
DROP TABLE IF EXISTS oauth_accounts;
DROP TABLE IF EXISTS users;
