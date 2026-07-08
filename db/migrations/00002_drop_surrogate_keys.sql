-- +goose Up

ALTER TABLE symbols DROP CONSTRAINT symbols_pkey;
ALTER TABLE symbols ADD CONSTRAINT symbols_pkey PRIMARY KEY USING INDEX idx_symbols_code;
ALTER TABLE symbols DROP COLUMN id;

ALTER TABLE candles DROP CONSTRAINT candles_pkey;
ALTER TABLE candles ADD CONSTRAINT candles_pkey PRIMARY KEY USING INDEX candle_sym_int_time;
ALTER TABLE candles DROP COLUMN id;

ALTER TABLE oauth_accounts DROP CONSTRAINT oauth_accounts_pkey;
ALTER TABLE oauth_accounts ADD CONSTRAINT oauth_accounts_pkey PRIMARY KEY USING INDEX idx_oauth_provider_uid;
ALTER TABLE oauth_accounts DROP COLUMN id;

-- +goose Down

ALTER TABLE oauth_accounts ADD COLUMN id BIGSERIAL;
ALTER TABLE oauth_accounts DROP CONSTRAINT oauth_accounts_pkey;
ALTER TABLE oauth_accounts ADD PRIMARY KEY (id);
CREATE UNIQUE INDEX idx_oauth_provider_uid ON oauth_accounts (provider, provider_uid);

ALTER TABLE candles ADD COLUMN id BIGSERIAL;
ALTER TABLE candles DROP CONSTRAINT candles_pkey;
ALTER TABLE candles ADD PRIMARY KEY (id);
CREATE UNIQUE INDEX candle_sym_int_time ON candles (symbol_code, "interval", "time");

-- symbols_pkey（現在はcode）を candles/watchlists のFKが参照しているため、
-- CASCADEで一旦FKごと落とし、id移行後に再作成する。
ALTER TABLE symbols ADD COLUMN id BIGSERIAL;
ALTER TABLE symbols DROP CONSTRAINT symbols_pkey CASCADE;
ALTER TABLE symbols ADD PRIMARY KEY (id);
CREATE UNIQUE INDEX idx_symbols_code ON symbols (code);

ALTER TABLE candles
    ADD CONSTRAINT fk_candles_symbol FOREIGN KEY (symbol_code) REFERENCES symbols(code) ON DELETE RESTRICT;
ALTER TABLE watchlists
    ADD CONSTRAINT fk_watchlists_symbol FOREIGN KEY (symbol_code) REFERENCES symbols(code) ON DELETE RESTRICT;
