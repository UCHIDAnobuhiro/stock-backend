-- +goose Up

ALTER TABLE users RENAME COLUMN password TO password_hash;

-- +goose Down

ALTER TABLE users RENAME COLUMN password_hash TO password;
