-- Issue collaboration foundation (Wave 3A): the issue-owned domain only.
--
--   issues            + assignee_type/assignee_id (polymorphic ActorRef), project_ref
--   issue_comments    + parent_id (threading), author_type/author_id (ActorRef), seq (Timeline)
--   issue_runs        new (issue-owned AI work lifecycle; NOT operations, NOT execution_tickets)
--   issue_activities  new (append-only Timeline projection; NOT an event source)
--   issue_context_refs new (issue-scoped references; opaque ref_id, no cross-module FK)
--
-- No agents/teams/workflows/sim_* /runtime/notifications/websocket/PR/logs schema lives here:
-- those are later waves or other modules. This migration is purely additive over 0001..0006.

ALTER TABLE issues
  ADD COLUMN assignee_type text NOT NULL DEFAULT 'user' CHECK(assignee_type IN ('user','agent','team')),
  ADD COLUMN assignee_id uuid,
  ADD COLUMN project_ref uuid;

-- Backfill the polymorphic assignee from the legacy bare-user FK (only non-null rows).
UPDATE issues SET assignee_type = 'user', assignee_id = assignee_user_id WHERE assignee_user_id IS NOT NULL;

ALTER TABLE issue_comments
  ADD COLUMN parent_id uuid REFERENCES issue_comments(id),
  ADD COLUMN author_type text NOT NULL DEFAULT 'user' CHECK(author_type IN ('user','agent','team','system')),
  ADD COLUMN author_id uuid,
  ADD COLUMN seq bigint;

-- Human authors keep author_user_id; agent/team/system authors leave it NULL, hence drop NOT NULL.
-- The composite FK(tenant_id, author_user_id) still holds and simply skips NULL (MATCH SIMPLE).
ALTER TABLE issue_comments ALTER COLUMN author_user_id DROP NOT NULL;

-- Backfill the comment author ActorRef from the human-only column.
UPDATE issue_comments SET author_type = 'user', author_id = author_user_id;

-- Deterministic seq backfill: order by (created_at, id) within each issue. Random UUID ordering is a
-- one-time migration concern only; the per-issue seq is authoritative thereafter and is shared with
-- issue_activities (see issue_activities.go nextTimelineSeq).
UPDATE issue_comments SET seq = sub.rn FROM (
  SELECT id, row_number() OVER (PARTITION BY issue_id ORDER BY created_at, id) AS rn
  FROM issue_comments
) sub WHERE issue_comments.id = sub.id;

ALTER TABLE issue_comments ALTER COLUMN seq SET NOT NULL;
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_issue_seq_uniq UNIQUE(issue_id, seq);

-- Runs: one issue may have many runs. executor_ref is a polymorphic external reference; the executor
-- system does not exist in the issue schema. terminal status (completed/failed/cancelled) is normal
-- history (deleted_at IS NULL); deleted_at is reserved for future hide/archive/admin cleanup.
CREATE TABLE issue_runs (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  issue_id uuid NOT NULL REFERENCES issues(id),
  version bigint NOT NULL DEFAULT 1 CHECK(version > 0),
  executor_type text NOT NULL CHECK(executor_type IN ('agent','team','workflow')),
  executor_id uuid NOT NULL,
  external_execution_id text NOT NULL DEFAULT '',
  execution_context_ref uuid,
  workflow_invocation_ref uuid,
  trigger_evidence_kind text NOT NULL DEFAULT '',
  trigger_evidence_ref_id uuid,
  status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','dispatched','running','completed','failed','cancelled','deferred')),
  parent_run_id uuid REFERENCES issue_runs(id),
  retry_of_run_id uuid REFERENCES issue_runs(id),
  rerun_of_run_id uuid REFERENCES issue_runs(id),
  delegated_from_run_id uuid REFERENCES issue_runs(id),
  attempt bigint NOT NULL DEFAULT 1 CHECK(attempt > 0),
  max_attempts bigint NOT NULL DEFAULT 1 CHECK(max_attempts > 0),
  input jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(input) = 'object'),
  result jsonb,
  error text NOT NULL DEFAULT '',
  failure_reason text NOT NULL DEFAULT '',
  trigger_summary text NOT NULL DEFAULT '',
  queued_at timestamptz NOT NULL DEFAULT now(),
  dispatched_at timestamptz,
  started_at timestamptz,
  completed_at timestamptz,
  fire_at timestamptz,
  lease_expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);
CREATE INDEX issue_run_list ON issue_runs(issue_id, created_at, id);
-- Pending dedup: at most one queued/dispatched run per (issue, executor_type, executor_id).
CREATE UNIQUE INDEX issue_run_pending_uniq ON issue_runs(issue_id, executor_type, executor_id)
  WHERE status IN ('queued','dispatched') AND deleted_at IS NULL;

-- Activities: append-only Timeline projection shared seq namespace with issue_comments.
CREATE TABLE issue_activities (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  issue_id uuid NOT NULL REFERENCES issues(id),
  seq bigint NOT NULL,
  actor_type text NOT NULL CHECK(actor_type IN ('user','agent','team','system')),
  actor_id uuid,
  action text NOT NULL CHECK(length(action) BETWEEN 1 AND 200),
  details jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(details) = 'object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(issue_id, seq)
);
CREATE INDEX issue_activity_list ON issue_activities(issue_id, seq);

-- Context refs: issue-scoped pointers to external resources; ref_id is opaque (no FK).
CREATE TABLE issue_context_refs (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  issue_id uuid NOT NULL REFERENCES issues(id),
  ref_type text NOT NULL CHECK(ref_type IN ('parent_issue','run','timeline_message','pull_request','project','workspace','acceptance_criteria')),
  ref_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(issue_id, ref_type, ref_id)
);
CREATE INDEX issue_context_ref_list ON issue_context_refs(issue_id, ref_type, id);