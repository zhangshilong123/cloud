-- Issue interaction input (Wave 3B-2): the confirmed, Issue-owned configuration of a collaboration
-- interaction — for Form Mode, the form values the user confirmed (§38.29).
--
--   issue_interactions.input  =  what the user configured  (Issue-owned, persisted here)
--   issue_runs.input          =  what the executor received (the effective execution snapshot)
--
-- Deliberately ONE generic column: it carries no Workflow-specific field, no status enum (the
-- confirm marker is `run_id IS NULL` -> `run_id IS NOT NULL`, §38.6) and no version (the confirm is a
-- compare-and-set on `run_id IS NULL`, §38.22). Purely additive over 0001..0008; edits no applied
-- migration.

ALTER TABLE issue_interactions
  ADD COLUMN input jsonb NOT NULL DEFAULT '{}'
  CHECK (jsonb_typeof(input) = 'object');
