-- +migrate Up
-- Placeholder retained after revert PR #1370 (which reverted #1367) to keep the
-- migration ledger consistent with environments that already applied this id.
-- The PR #1367 schema change had already been demoted to a no-op before merge,
-- so this file is intentionally empty.
SELECT 1;

-- +migrate Down
SELECT 1;
