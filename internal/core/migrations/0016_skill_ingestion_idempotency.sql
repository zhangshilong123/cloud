-- Cloud Skills ingestion idempotency and recovery (canonical ingestion saga, Phase 2).
--
-- Four additive changes to skill_ingestions, following
-- specs/decisions/cloud/skills/20260927-canonical-ingestion-saga.md (Schema section). This file
-- must never edit 0015_skills.sql, which is applied and immutable.
--
--   canonical_name       the candidate's stable identity component (D6/D11): part of the
--                        per-candidate idempotency namespace (workspace_id, idempotency_key,
--                        canonical_name). Nullable here because Step 1A's
--                        integration/skill_persistence_test.go rows predate this column; the
--                        ingestion pipeline always writes a non-empty value, and a later forward
--                        migration may tighten it to NOT NULL.
--   request_fingerprint  the semantic request fingerprint (D10): SHA-256 hex over the
--                        domain-separated, length-prefixed (target_skill_id, display_name,
--                        summary, content_digest, package_digest). It is the durable replay-vs-
--                        conflict discriminator (D22), distinct from the idempotency key.
--   activation_outcome   the durable activation-CAS result (D20): 'activated' when the CAS
--                        advanced current_revision_id, 'activation_conflict' when it did not.
--                        NULL for every non-committed state.
--   unique index         the deterministic, concurrency-safe per-candidate idempotency identity
--                        (D11); the recovery path depends on it to find an existing ingestion.
--
-- NULLs compare distinct in a unique index, so the historical/test rows without canonical_name do
-- not collide with one another.

ALTER TABLE skill_ingestions ADD COLUMN canonical_name text
    CHECK (canonical_name IS NULL OR length(canonical_name) BETWEEN 1 AND 200);
ALTER TABLE skill_ingestions ADD COLUMN request_fingerprint text
    CHECK (request_fingerprint IS NULL OR length(request_fingerprint) BETWEEN 1 AND 128);
ALTER TABLE skill_ingestions ADD COLUMN activation_outcome text
    CHECK (activation_outcome IS NULL OR activation_outcome IN ('activated','activation_conflict'));
CREATE UNIQUE INDEX skill_ingestion_candidate_uniq
    ON skill_ingestions(workspace_id, idempotency_key, canonical_name);
