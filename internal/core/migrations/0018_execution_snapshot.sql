-- Cloud Skills Execution snapshot: logical Execution, physical Attempt and immutable
-- ExecutionSkillBinding (Phase 5).
--
-- Mirrors specs/decisions/cloud/skills/20260927-execution-skill-snapshot.md. This file must never
-- edit 0015_skills.sql / 0016_skill_ingestion_idempotency.sql / 0017_agents_and_skill_bindings.sql,
-- which are applied and immutable.
--
--   executions                 logical execution identity: immutable input plus the frozen binding
--                              set. Owned by one Collaboration Workspace and admitted for one
--                              Agent; the caller only ever supplies agent_id, never a Skill list
--                              (ADR D3 / Agent ADR D8).
--   attempts                   physical execution attempts. The first Attempt is created atomically
--                              with its Execution in the same admission transaction (ADR D3), in a
--                              durable `eligible` state with empty node_id / lease / epoch /
--                              dispatch metadata. retry adds a new Attempt (ordinal increments);
--                              the Controller only claims/dispatches an existing Attempt.
--   execution_skill_bindings   immutable execution fact: the exact SkillRevision each Execution
--                              froze at admission (skill_id + skill_revision_id + denormalized
--                              content identity). Written once, never updated; no bearer-credential
--                              field is created (capability is never persisted).
--
-- Deliberately NOT created here: any RetrievalCapability / signed-URL / object-locator field, and
-- the dispatch/claim/lease plumbing (Phase 6+). The Execution registry itself is the durable
-- PostgreSQL authority that later capability issuance is authorized against.

CREATE TABLE executions (
 execution_id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 workspace_id uuid NOT NULL REFERENCES collab_workspaces(id),
 agent_id uuid NOT NULL REFERENCES agents(id),
 actor_user_id uuid NOT NULL REFERENCES users(id),
 input jsonb NOT NULL DEFAULT '{}'::jsonb,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX execution_workspace_list ON executions(workspace_id, created_at, execution_id);
CREATE INDEX execution_agent_list ON executions(agent_id, created_at, execution_id);

CREATE TABLE attempts (
 attempt_id uuid PRIMARY KEY,
 execution_id uuid NOT NULL REFERENCES executions(execution_id),
 ordinal integer NOT NULL CHECK(ordinal>=1),
 state text NOT NULL DEFAULT 'eligible' CHECK(state IN ('eligible','dispatched','running','succeeded','failed','canceled','superseded')),
 node_id text,
 result jsonb,
 dispatched_epoch bigint,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(execution_id, ordinal)
);
CREATE INDEX attempt_execution_list ON attempts(execution_id, ordinal);

-- one Execution + one Skill => one frozen binding (PRIMARY KEY), and skill_revision_id may only name
-- a revision owned by that very Skill (composite FK, the same guard as skills.current_revision_id).
-- The denormalized content_digest / size_bytes / package_format(_version) let dispatch and later
-- retrieval reference the frozen revision without re-joining or re-resolving mutable Skill state.
CREATE TABLE execution_skill_bindings (
 execution_id uuid NOT NULL REFERENCES executions(execution_id),
 skill_id uuid NOT NULL REFERENCES skills(id),
 skill_revision_id uuid NOT NULL,
 content_digest text NOT NULL CHECK(length(content_digest) BETWEEN 1 AND 128),
 size_bytes bigint NOT NULL CHECK(size_bytes>=0),
 package_format text NOT NULL CHECK(length(package_format) BETWEEN 1 AND 64),
 package_format_version integer NOT NULL CHECK(package_format_version>=1),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (execution_id, skill_id),
 FOREIGN KEY (skill_revision_id, skill_id) REFERENCES skill_revisions(id, skill_id)
);

-- ExecutionSkillBinding is an immutable execution fact: there is no in-place UPDATE. DELETE is only
-- ever an Execution-level cleanup (future GC/retention), never a per-row mutation, so it is not
-- blocked here. A bind once written stays byte-identical for the life of the Execution.
CREATE FUNCTION execution_skill_binding_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'execution skill binding is immutable' USING ERRCODE='23514';
 RETURN NULL;
END $$;
CREATE TRIGGER execution_skill_binding_immutable BEFORE UPDATE ON execution_skill_bindings
 FOR EACH ROW EXECUTE FUNCTION execution_skill_binding_immutable();
