-- Cloud Skills delivery metadata plumbing (Phase 6A.1).
--
-- Freezes the trusted delivery integrity metadata approved by Phase 6A.0 (Option A) so the Node's
-- required byte-verification chain (`SHA256(package bytes) == package_digest` AND
-- `digest of decoded tree == content_digest`) can be driven from durable, immutable revision
-- metadata instead of object-key parsing, a signed URL, the frontend, or a mutable current-revision
-- lookup. This file is append-only and never edits 0015_skills.sql / 0018_execution_snapshot.sql.
--
-- Empty-algorithm convention: `digest_algorithm` on skill_revisions already names the *content/tree*
-- digest algorithm (used in UNIQUE(skill_id, digest_algorithm, content_digest)). The exact
-- package-byte digest is a different layer and gets its OWN explicit columns (package_digest,
-- package_digest_algorithm) so the two digest layers are never conflated (object-storage ADR D4;
-- content_digest != package_digest). Both are SHA-256 in v1, but the schema keeps the distinction
-- structural so a future package format that separates the two algorithms needs no new migration.
--
-- Legacy-row rollout (see plan Phase 6A.1 §4): every column is added NULLABLE because rows created
-- before 0019 have no durable package digest, and a migration must never recover it by parsing the
-- object_locator key or touching Object Storage (that would violate the §7 authority rule). New
-- writes require the value; the core admission path fails closed when the required trusted
-- package_digest is absent. A backfill of pre-0019 rows, if ever needed, is a separate runtime
-- mechanism (re-ingest / verified recompute), never a migration SQL that fabricates a digest.

-- skill_revisions: durable, immutable, authoritative package-byte digest of the exact
-- ora-skill-package container (object-storage ADR D4's package_digest), plus its algorithm.
ALTER TABLE skill_revisions
 ADD COLUMN package_digest text CHECK(package_digest IS NULL OR length(package_digest) BETWEEN 1 AND 128),
 ADD COLUMN package_digest_algorithm text NOT NULL DEFAULT 'sha256'
   CHECK(length(package_digest_algorithm) BETWEEN 1 AND 64);

-- execution_skill_bindings: the frozen execution snapshot now carries the full delivery digest
-- metadata the Controller/Node need to byte-verify and to form the Node cache identity. The
-- existing skill_revision_id / content_digest / size_bytes / package_format(_version) columns
-- are already part of the snapshot; we add the content digest_algorithm (previously only on
-- skill_revisions) plus the package-byte digest and its algorithm. The table's immutability
-- trigger (0018) continues to forbid in-place UPDATE, so these are written once at admission.
-- They stay nullable for pre-0019 bindings; new admissions always populate them and fail closed
-- if the frozen revision cannot.
ALTER TABLE execution_skill_bindings
 ADD COLUMN digest_algorithm text CHECK(digest_algorithm IS NULL OR length(digest_algorithm) BETWEEN 1 AND 64),
 ADD COLUMN package_digest text CHECK(package_digest IS NULL OR length(package_digest) BETWEEN 1 AND 128),
 ADD COLUMN package_digest_algorithm text NOT NULL DEFAULT 'sha256'
   CHECK(length(package_digest_algorithm) BETWEEN 1 AND 64);

CREATE INDEX execution_skill_bindings_delivery_digest
 ON execution_skill_bindings(package_digest) WHERE package_digest IS NOT NULL;