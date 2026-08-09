-- +goose Up
-- +goose StatementBegin
-- A waiting occurrence has no run yet. Keep its redacted blocked plan and
-- deadline on the occurrence so process restarts and River retries preserve
-- the exact producer deployment selected when waiting began. The persisted
-- status remains `pending` for compatibility with the original table check;
-- the scheduler domain exposes `waiting_prerequisites` whenever these fields
-- are present.
ALTER TABLE schedule_occurrences
    ADD COLUMN prerequisite_plan TEXT
        CHECK (prerequisite_plan IS NULL OR json_valid(prerequisite_plan));
ALTER TABLE schedule_occurrences
    ADD COLUMN prerequisite_deadline TEXT;
ALTER TABLE schedule_occurrences
    ADD COLUMN prerequisite_reason TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE schedule_occurrences DROP COLUMN prerequisite_reason;
ALTER TABLE schedule_occurrences DROP COLUMN prerequisite_deadline;
ALTER TABLE schedule_occurrences DROP COLUMN prerequisite_plan;
-- +goose StatementEnd
