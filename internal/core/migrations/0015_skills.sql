-- Cloud Skills workspace-owned domain and persistence foundation.
--
-- Phase 2 / Step 1A introduces exactly three first-class Skill resources, following
-- specs/decisions/cloud/skills/0-cloud-skills.md:
--
--   skills           mutable business resource owned by exactly one Collaboration Workspace
--                    (collab_workspaces — the collaboration/authorization boundary; never the
--                    runtime `workspaces` table);
--   skill_revisions  immutable canonical content snapshots belonging to one Skill;
--   skill_ingestions durable upload/import saga evidence (journal-first), kept strictly
--                    separate from SkillRevision so transient failure states never leak into
--                    the immutable content model.
--
-- Deliberately NOT created here (later steps): AgentSkillBinding, ExecutionSkillBinding, and any
-- Object Storage schema. PostgreSQL stores business state only; immutable package bytes belong to
-- Object Storage (D8). The signed RetrievalCapability of D30 is a bearer credential that must never
-- be persisted, so no capability column or table exists here.

CREATE TABLE skills (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES collab_workspaces(id),
 canonical_name text NOT NULL CHECK(length(canonical_name) BETWEEN 1 AND 200),
 display_name text NOT NULL CHECK(length(display_name) BETWEEN 1 AND 200),
 summary text NOT NULL DEFAULT '',
 current_revision_id uuid,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 deleted_at timestamptz
);
-- Active Skill canonical names are unique within one Collaboration Workspace. Soft-deleting a Skill
-- removes it from this index, so a replacement may reuse the same canonical name without the
-- archived row blocking it (the repository's partial-unique-index convention).
CREATE UNIQUE INDEX skill_active_name_uniq ON skills(workspace_id, canonical_name) WHERE deleted_at IS NULL;
CREATE INDEX skill_workspace_list ON skills(workspace_id, created_at, id);

-- SkillRevision is append-only: it deliberately has no version, updated_at, or deleted_at, so the
-- supported domain model cannot express an in-place mutation. Content change always yields a new
-- revision row (or reuses an identical one via UNIQUE(skill_id,digest_algorithm,content_digest)).
CREATE TABLE skill_revisions (
 id uuid PRIMARY KEY,
 skill_id uuid NOT NULL REFERENCES skills(id),
 content_digest text NOT NULL CHECK(length(content_digest) BETWEEN 1 AND 128),
 digest_algorithm text NOT NULL DEFAULT 'sha256' CHECK(length(digest_algorithm) BETWEEN 1 AND 64),
 size_bytes bigint NOT NULL DEFAULT 0 CHECK(size_bytes>=0),
 file_count integer NOT NULL DEFAULT 0 CHECK(file_count>=0),
 package_format text NOT NULL DEFAULT 'ora-skill-package' CHECK(length(package_format) BETWEEN 1 AND 64),
 package_format_version integer NOT NULL DEFAULT 1 CHECK(package_format_version>=1),
 object_locator text NOT NULL DEFAULT '' CHECK(length(object_locator) <= 1024),
 package_name text NOT NULL DEFAULT '' CHECK(length(package_name) <= 200),
 package_description text NOT NULL DEFAULT '',
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 -- (id, skill_id) backs the current_revision foreign key below and rules out a pointer to a
 -- revision owned by a different Skill. (skill_id, digest_algorithm, content_digest) enforces one
 -- immutable revision per canonical digest within a Skill (D6); equal content in two different
 -- Skills remains two independent business revisions (D7).
 UNIQUE(id, skill_id),
 UNIQUE(skill_id, digest_algorithm, content_digest)
);
-- current_revision_id may only name a revision owned by this very Skill. It is nullable during
-- provisioning (the Skill is created before its first revision is activated) and is set in a short
-- database-only transaction after the immutable object is stored.
ALTER TABLE skills ADD FOREIGN KEY(current_revision_id, id) REFERENCES skill_revisions(id, skill_id);
CREATE INDEX skill_revision_list ON skill_revisions(skill_id, created_at, id);

-- Ordinary updates must never change a Skill's owning Workspace: cross-Workspace moves are modelled
-- as copy/export/import (a new Skill), never an in-place ownership edit (D1, Section 4).
CREATE FUNCTION skill_immutable_ownership() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id<>OLD.id OR NEW.workspace_id<>OLD.workspace_id THEN
  RAISE EXCEPTION 'skill workspace ownership is immutable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER skill_immutable BEFORE UPDATE ON skills FOR EACH ROW EXECUTE FUNCTION skill_immutable_ownership();

-- SkillIngestion is the journal-first saga record for one candidate: it persists the intent and
-- external-object evidence before the Object Storage mutation and the result afterwards. It never
-- shares a transaction, row lock, or advisory lock with Object Storage work (D19/D30). States are
-- the Cloud-side naming for the required stages (intent -> external pending -> verified -> committed
-- -> failed / reconcile-required), not a SkillRevision lifecycle.
CREATE TABLE skill_ingestions (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES collab_workspaces(id),
 target_skill_id uuid REFERENCES skills(id),
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 200),
 source_type text NOT NULL CHECK(source_type IN ('directory','archive')),
 expected_digest text CHECK(expected_digest IS NULL OR length(expected_digest) BETWEEN 1 AND 128),
 digest_algorithm text CHECK(digest_algorithm IS NULL OR length(digest_algorithm) BETWEEN 1 AND 64),
 object_locator text CHECK(object_locator IS NULL OR length(object_locator) BETWEEN 1 AND 1024),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','storing','verified','committed','failed')),
 error_code text CHECK(error_code IS NULL OR length(error_code) BETWEEN 1 AND 128),
 error_detail text,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX skill_ingestion_workspace_list ON skill_ingestions(workspace_id, created_at, id);
CREATE INDEX skill_ingestion_target ON skill_ingestions(target_skill_id) WHERE target_skill_id IS NOT NULL;