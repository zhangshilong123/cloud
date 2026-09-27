-- Cloud Skills Agent authority and AgentSkillBinding (Phase 4).
--
-- Mirrors specs/decisions/cloud/agent/0-agent-skill-binding.md. This file must never edit
-- 0015_skills.sql / 0016_skill_ingestion_idempotency.sql, which are applied and immutable.
--
--   agents                 durable Agent business resource owned by exactly one Collaboration
--                          Workspace (collab_workspaces — the same ownership boundary as skills;
--                          never the runtime `workspaces` table). Soft-deleted (deleted_at), with
--                          immutable workspace ownership and active-name uniqueness per workspace.
--   agent_skill_bindings   the mutable future-execution configuration: which Skills an Agent
--                          defaults to. It references Skill business identity (skill_id) only,
--                          NEVER a SkillRevision / digest / locator (ADR D3). A thin association
--                          with a membership-style composite PK: remove = DELETE, no soft delete.
--                          Historical correctness is carried by the immutable ExecutionSkillBinding
--                          (Phase 5), not by this table (ADR D6).
--
-- Deliberately NOT created here: executions / attempts / execution_skill_bindings, and any capability
-- or signed-URL / object-locator field. Those belong to the Execution snapshot (Phase 5) and the
-- RetrievalCapability layers; a bearer credential must never be persisted.

CREATE TABLE agents (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES collab_workspaces(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
 status text NOT NULL DEFAULT 'active' CHECK(status IN ('active','disabled')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 deleted_at timestamptz
);
-- Active Agent names are unique within one Collaboration Workspace; a soft-deleted Agent releases its
-- name (the repository's partial-unique-index convention, mirroring skills.skill_active_name_uniq).
CREATE UNIQUE INDEX agent_active_name_uniq ON agents(workspace_id, name) WHERE deleted_at IS NULL;
CREATE INDEX agent_workspace_list ON agents(workspace_id, created_at, id);

-- Ordinary updates must never change an Agent's owning Workspace: cross-Workspace moves are modelled
-- as copy/export/import (a new Agent), never an in-place ownership edit (ADR D1/D2). Mirrors
-- skill_immutable_ownership.
CREATE FUNCTION agent_immutable_ownership() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id<>OLD.id OR NEW.workspace_id<>OLD.workspace_id THEN
  RAISE EXCEPTION 'agent workspace ownership is immutable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER agent_immutable BEFORE UPDATE ON agents FOR EACH ROW EXECUTE FUNCTION agent_immutable_ownership();

-- one Agent + one Skill => at most one binding, enforced by the composite PK (ADR D3). enabled=false
-- keeps the configuration but excludes it from effective execution selection (ADR D5); remove = DELETE.
CREATE TABLE agent_skill_bindings (
 agent_id uuid NOT NULL REFERENCES agents(id),
 skill_id uuid NOT NULL REFERENCES skills(id),
 enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (agent_id, skill_id)
);
CREATE INDEX agent_skill_binding_list ON agent_skill_bindings(agent_id, enabled, skill_id);