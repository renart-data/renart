-- +goose Up
ALTER TABLE renart_snapshots
    ADD COLUMN dependency_manifest TEXT NOT NULL
    DEFAULT '{"version":1,"dependencies":[]}';

-- +goose Down
ALTER TABLE renart_snapshots DROP COLUMN dependency_manifest;
