-- Issue collaboration interaction spine (Wave 3B-1): the minimal, evolvable record of a
-- collaboration interaction, one row per selected @ target.
--
--   Comment  !=  Interaction  !=  IssueRun
--
-- A single comment may carry 0..N targets; each target becomes one `issue_interactions` row.
-- `target_type='user'` is a Human Mention (mode='mention', no run); `agent`/`team` are Task Mode
-- (mode='task', `task` must be non-empty, `run_id` links the initial issue-owned run); `workflow`
-- is Form Mode (mode='form') and is not executed this wave. This is deliberately a *separate row*
-- next to the comment — never a widening of `issue_comments` (§37.8) and never a `comment.run_id`.
--
-- Purely additive over 0001..0007; edits no applied migration.

CREATE TABLE issue_interactions (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  issue_id uuid NOT NULL REFERENCES issues(id),
  comment_id uuid NOT NULL REFERENCES issue_comments(id),
  target_type text NOT NULL CHECK(target_type IN ('user','agent','team','workflow')),
  target_id uuid NOT NULL,
  mode text NOT NULL CHECK(mode IN ('mention','task','form')),
  task text NOT NULL DEFAULT '',
  run_id uuid,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX issue_interaction_issue ON issue_interactions(issue_id, created_at, id);
CREATE INDEX issue_interaction_comment ON issue_interactions(comment_id);