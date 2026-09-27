# Ora Cloud Skills — Implementation Plan

Status: **Approved design baseline / ready for implementation**

Purpose: this file is the execution, audit, and acceptance basis for the Cloud Skills work.
An implementation agent MUST read this file first, then the nearest `AGENTS.md`, relevant approved ADRs/specs/tests/docs, before editing code.

---

## 0. Operating protocol

Implementation workflow:

1. Read this `plan.md`.
2. Read repository rules:
   - root `AGENTS.md`
   - `specs/AGENTS.md` before any `specs/` change
   - `frontend/AGENTS.md` before any `frontend/` change
   - nearest nested `AGENTS.md` files
3. Inspect relevant approved ADRs, core test cases, current implementation, migrations, API contracts, and existing tests.
4. Implement the smallest coherent change that satisfies this plan.
5. If implementation conflicts with this plan, an approved ADR, repository rules, an immutable compatibility boundary, or current authoritative implementation facts, **STOP and report the conflict. Do not silently reinterpret the plan.**
6. Ordinary implementation details may be chosen autonomously.
7. Any semantic change involving schema, ownership, authorization, transaction boundaries, external effects, recovery, idempotency, compatibility, execution inputs, or protected behavior requires discussion and an update to this plan before implementation proceeds.
8. Run all applicable repository gates.
9. Audit the final diff against this plan and report each acceptance item as PASS / FAIL / NOT APPLICABLE.

This plan does not override repository `AGENTS.md`, approved ADRs, or compatibility requirements.

---

# 1. Goal

Add first-class **Cloud Skills** with:

- Collaboration Workspace ownership.
- Directory and archive import.
- Recursive discovery of multiple Skills.
- Immutable Skill content revisions in Object Storage.
- PostgreSQL business metadata and recovery evidence.
- Persistent Agent Skill assignment.
- Immutable per-Execution SkillRevision snapshots.
- Direct Node retrieval using short-lived read-only retrieval capabilities.
- Verified Node content cache.
- Per-Attempt Agent-native Skill materialization.
- Hard pre-spawn readiness gating.
- Explicit retry semantics using immutable Execution inputs and new Attempts.

The design may use Multica implementation experience as evidence, but Ora must preserve Ora's own architecture and invariants.

Core principle:

> Migrate capability, not architecture.

---

# 2. Terminology and ownership boundaries

The implementation MUST distinguish these concepts explicitly.

## 2.1 Collaboration Workspace

Product collaboration / authorization boundary.

- A Skill belongs to exactly one Collaboration Workspace.
- Workspace ownership is resolved and enforced in Cloud.
- A Skill cannot move between Collaboration Workspaces via ordinary update.
- Transfer between Workspaces means copy/export/import and creates a distinct business resource.

Do not confuse Collaboration Workspace with Runtime Workspace or Desktop `EffectScope::Workspace`.

## 2.2 Runtime Workspace

Project-scoped runtime checkout/worktree/runtime lifecycle concept.

Cloud Skills ownership MUST NOT be assigned to Runtime Workspace.

## 2.3 Skill

Stable mutable business resource owned by one Collaboration Workspace.

## 2.4 SkillRevision

Immutable canonical package snapshot belonging to one Skill.

## 2.5 AgentSkillBinding

Mutable Agent configuration describing which Skills future Executions should use by default.

## 2.6 ExecutionSkillBinding

Immutable execution fact describing the exact SkillRevision used by one Execution.

## 2.7 Execution

Logical execution with immutable resolved inputs.

## 2.8 Attempt

One physical attempt to execute an Execution.

Retries create new Attempts under the same Execution. They do not implicitly create a new Execution or re-resolve mutable Agent/Skill configuration.

## 2.9 SkillIngestion

Durable upload/import saga evidence for one candidate Skill.

## 2.10 VerifiedSkillBundle

Node-local verified immutable content derived from a SkillRevision. It is not authoritative business state.

## 2.11 ExecutionSkillProjection

Attempt-scoped Agent-visible filesystem projection derived from verified Skill content.

---

# 3. Authoritative persistence and storage

## 3.1 PostgreSQL

PostgreSQL is authoritative for:

- Skill business metadata.
- SkillRevision metadata.
- AgentSkillBinding.
- ExecutionSkillBinding.
- SkillIngestion/recovery evidence.
- Execution / Attempt durable state.
- authorization/lifecycle/version/idempotency metadata.

PostgreSQL MUST NOT be used to store arbitrary Skill package bytes.

## 3.2 Object Storage

Object Storage is authoritative for immutable canonical Skill package bytes.

Objects MUST be immutable once published.

Do not overwrite an existing immutable revision object.

## 3.3 Node filesystem

Node cache, staging directories, and execution projections are derived state.

Loss of all Node-local Skill data MUST NOT corrupt Cloud business state.

---

# 4. Skill model

Recommended semantic shape:

```text
Skill
├─ id
├─ workspace_id
├─ canonical_name
├─ display_name
├─ summary
├─ icon / tags / board metadata as supported by repository conventions
├─ current_revision_id
├─ version
├─ created_by
├─ created_at
├─ updated_at
└─ deleted_at
```

Requirements:

- exactly one Collaboration Workspace owner;
- mutable business/UI metadata;
- soft delete;
- optimistic concurrency using existing repository conventions;
- `display_name` does not define package identity;
- `canonical_name` is derived from validated `SKILL.md.name`;
- changing board/UI metadata MUST NOT mutate a SkillRevision or `SKILL.md`;
- ordinary update MUST NOT change `workspace_id`.

---

# 5. Workspace Skill identity and matching

## 5.1 Active-name uniqueness

Within one Collaboration Workspace, active Skills MUST have unique canonical package names.

Semantic constraint:

```text
(workspace_id, canonical_name)
unique among non-deleted Skills
```

Use the repository-supported PostgreSQL mechanism for this invariant.

## 5.2 Batch import matching

For directory/archive batch import:

```text
(workspace_id, canonical SKILL.md.name)
```

is the Skill matching key.

If no active Skill matches:

- create a new Skill;
- create/reuse its first SkillRevision as appropriate;
- activate the resulting revision.

If an active Skill matches:

- treat the candidate as new content for that existing Skill.

## 5.3 Explicit update matching

For an explicit "update this Skill" operation:

```text
target_skill_id
```

is authoritative.

A changed `SKILL.md.name` does not cause heuristic rename detection.

The update must still satisfy same-Workspace canonical-name uniqueness.

## 5.4 No heuristic rename inference

Do not infer rename based on directory path, similar description, similar bytes, content similarity, or prior display name.

## 5.5 Display name

`display_name` is mutable Workspace metadata and MUST NOT participate in import matching.

---

# 6. SkillRevision model and deduplication

Recommended semantic shape:

```text
SkillRevision
├─ id
├─ skill_id
├─ workspace_id             # optional denormalization if useful for auth/query
├─ content_digest
├─ digest_algorithm
├─ size_bytes
├─ file_count
├─ package_format
├─ package_format_version
├─ object_locator
├─ package_name
├─ package_description
├─ manifest metadata
├─ created_by
└─ created_at
```

Requirements:

- immutable after creation/ready state;
- no update-content API;
- content changes create/reuse another immutable revision;
- revision identity belongs to a specific Skill.

Strong uniqueness invariant:

```text
UNIQUE(skill_id, digest_algorithm, content_digest)
```

## 6.1 Same Skill + same digest

Reuse the existing SkillRevision. Do not create duplicate revision rows.

Repeated imports may create separate SkillIngestion records, but not duplicate SkillRevision records.

## 6.2 Re-importing an older revision

If `R1 = digest A`, `R2 = digest B (current)`, and digest A is imported again:

- reuse R1;
- activation may move `current_revision_id` back to R1;
- do not create R3 with the same bytes as R1.

## 6.3 Different Skills + same digest

Different Skill business resources remain distinct. Physical object storage and Node caches MAY deduplicate by digest, but business identity/ownership MUST NOT merge.

---

# 7. SkillIngestion and batch import

Skill upload/import is a saga, not a transaction spanning PostgreSQL and Object Storage.

A candidate Skill uses its own SkillIngestion and its own short database transactions.

Conceptual fields:

```text
SkillIngestion
├─ id
├─ workspace_id
├─ target_skill_id?
├─ idempotency_key
├─ source_type
├─ expected/canonical digest as known
├─ stable object identity / locator metadata
├─ state
├─ error_code?
├─ error_detail?
├─ created_by
├─ created_at
└─ updated_at
```

Exact state names may follow repository conventions, but semantics must cover intent recorded, external object work pending/in progress, content verified, revision committed/activated, and failed/recoverable evidence.

Do not put transient ingestion failure states into immutable SkillRevision rows.

## 7.1 Batch behavior

Recursive directory/archive import uses:

```text
DISCOVER
→ VALIDATE CANDIDATES
→ APPLY EACH CANDIDATE INDEPENDENTLY
→ BATCH SUMMARY
```

Batch semantics are **partial success**.

One invalid candidate MUST NOT roll back other successful candidates.

A batch-level database transaction spanning all Skills is prohibited.

A persistent batch table is not required by this plan unless existing API/lifecycle conventions make it necessary. Per-candidate ingestion evidence is required.

Batch response/reporting must summarize at least discovered candidates, created, updated/activated, unchanged, and failed.

---

# 8. Upload/import discovery

Accepted V1 sources:

- local directory selection;
- archive upload.

Both MUST normalize into the same canonical virtual file tree and canonical package representation.

## 8.1 Recursive directory discovery

Recursively find `SKILL.md`.

The direct parent of each `SKILL.md` is one candidate Skill root. The entire subtree under that root belongs to the candidate.

Nested Skill roots are unsupported in V1 and MUST fail validation rather than be guessed or merged.

## 8.2 Archive discovery

Archive entries are interpreted as a virtual tree and use the same Skill-root discovery semantics as directory import.

Do not extract untrusted archives directly to a final filesystem tree before validating entry paths/types/limits.

---

# 9. Canonical package contract

## 9.1 Root requirements

A canonical Skill package is an independent file tree whose root contains `SKILL.md`.

## 9.2 Canonical paths

Stored package paths MUST be relative POSIX-style paths.

Forbidden: absolute paths, `.` components, `..` components, empty path components, NUL, Windows drive prefixes, ambiguous separators, and path traversal.

Input Windows separators may be normalized at the ingestion boundary before canonical validation.

Normalize once, validate canonical form, and store only canonical form.

## 9.3 Collisions

After canonicalization:

- duplicate canonical paths are invalid;
- case-insensitive path collisions are invalid for portability across filesystems.

Canonical identity itself remains case-sensitive unless an approved repository/spec rule says otherwise.

## 9.4 Filesystem entry types

V1 supports only regular files and directories.

V1 rejects symlinks, hardlink archive entries, devices, FIFOs, sockets, and other special filesystem nodes.

## 9.5 Text and binary

`SKILL.md` must be valid UTF-8, parse required package metadata, contain valid `name`, and satisfy the accepted Skill metadata schema.

Other package files may contain arbitrary bytes.

## 9.6 No content rewriting during canonicalization

Canonicalization MUST NOT silently rewrite Markdown, reorder YAML/frontmatter, normalize newlines, inject a name, or reformat user files.

Canonicalization defines tree/path/manifest/package representation, not author-file formatting.

---

# 10. Canonical digest

Do not hash raw uploaded zip/tar bytes as Skill content identity.

Digest is computed from the canonical file tree.

Required properties:

- versioned digest scheme;
- per-file digest;
- canonical deterministic ordering by canonical path;
- unambiguous encoding such as length-prefixed fields or another explicitly canonical encoding.

Conceptual manifest:

```text
CanonicalManifest
├─ manifest_version
└─ files[]
   ├─ path
   ├─ size
   └─ sha256
```

Then:

```text
content_digest = SHA256(canonical-manifest-encoding)
```

Archive metadata such as mtime, uid/gid, entry order, compression method/level, and comments MUST NOT affect Skill content identity.

Directory input, ZIP input, and other future adapters producing identical canonical files/bytes must produce the same digest.

---

# 11. Canonical Object Storage package

The authoritative runtime object MUST be a versioned canonical Ora Skill package, not the user's raw transport archive.

Conceptual identity:

```text
package_format = ora-skill-package
package_format_version = 1
```

Exact serialization/container format may be chosen during implementation, but it MUST preserve arbitrary bytes, permit safe bounded materialization, be versioned, and be independently integrity-verifiable.

Node consumes canonical packages only and MUST NOT need to understand browser directory upload, ZIP upload, or other source transport formats.

---

# 12. Quotas and archive safety

V1 MUST enforce explicit configurable limits for at least:

- maximum files per Skill;
- maximum single file size;
- maximum total uncompressed Skill size;
- maximum `SKILL.md` size;
- maximum path length;
- maximum path component length;
- maximum compressed archive request size where applicable;
- maximum uncompressed archive output.

Limits must be enforced before a SkillRevision is committed/activated.

Archive processing MUST defend against traversal/zip-slip, absolute paths, special nodes, symlink/hardlink tricks, duplicate normalized paths, case-insensitive collisions, and decompression bombs.

Exact default numeric values are implementation details unless an existing repository/spec contract already defines them.

---

# 13. Upload/update transaction semantics

Never hold a PostgreSQL transaction or database lock across Object Storage/HTTP/filesystem/Node/process work.

Required saga shape:

```text
DB TX #1
  authorize
  validate stable request identity / Idempotency-Key
  persist ingestion intent and stable external identity/evidence
COMMIT

Object Storage
  upload canonical immutable object
  verify result / reconcile ambiguous outcome

DB TX #2
  create or reuse immutable SkillRevision
  finalize ingestion
  CAS Skill.current_revision_id using Skill.version
COMMIT
```

Requirements:

- stable object identity before external mutation;
- retry/reconcile ambiguous object-store results by stable identity;
- never guess success;
- never overwrite an immutable revision object;
- activation occurs only after verified content exists;
- activation conflict does not invalidate an otherwise valid immutable revision;
- external effects remain outside DB transactions.

POST/DELETE and optimistic concurrency must follow current repository conventions.

---

# 14. Agent Skill configuration

V1 selection model:

```text
Agent persistent Skill defaults only
```

No per-run add/remove Skill override in V1.

Conceptual binding:

```text
AgentSkillBinding
├─ agent_id
├─ skill_id
├─ enabled
├─ created_at
└─ updated_at
```

Requirements:

- binding references Skill business identity, not a revision;
- Agent configuration answers: "which Skills should future Executions use?";
- disabled binding remains configuration but is excluded from effective execution selection;
- binding does not contain runtime paths or object-store details.

---

# 15. Execution Skill snapshot

At Execution creation/admission time Cloud MUST:

1. authorize the caller and owning Collaboration Workspace;
2. resolve the Agent's enabled Skill assignments;
3. add platform-required Skills if such a policy exists;
4. resolve each selected Skill to an exact immutable current SkillRevision;
5. persist immutable ExecutionSkillBinding rows;
6. commit the execution input snapshot before dispatch.

Conceptual model:

```text
ExecutionSkillBinding
├─ execution_id
├─ skill_id
├─ skill_revision_id
├─ content_digest
├─ size_bytes
├─ package_format
├─ package_format_version
└─ created_at
```

Once committed, an ExecutionSkillBinding is immutable.

Strong invariants:

- Controller claim/dispatch MUST NOT recompute Skills from mutable AgentSkillBinding.
- Node MUST NOT resolve "current SkillRevision".
- Updating Agent Skill assignments affects only future Executions.
- Updating `Skill.current_revision_id` affects only future Executions.
- Soft deleting a Skill after snapshot creation MUST NOT silently change existing Execution inputs.
- retry preserves exact ExecutionSkillBinding rows.

---

# 16. Execution vs Attempt recovery model

Execution owns immutable business inputs. Attempt is a physical attempt.

```text
Execution E
├─ immutable inputs
├─ immutable ExecutionSkillBinding[]
├─ Attempt 1
├─ Attempt 2
└─ ...
```

Retry semantics:

```text
Retry
= new Attempt under same Execution
= same exact immutable inputs
```

`Run Again` / creating a new logical execution:

```text
= new Execution
= resolve current Agent configuration and current Skill revisions again
```

Do not conflate Retry and Run Again.

The first Attempt is created atomically with the Execution in the same admission transaction, in a durable `eligible`
state with empty node/lease/epoch/dispatch metadata. An admitted Execution always has its complete Skill snapshot and
its first Attempt together — there is no "admitted but not yet attempted" state. The Controller only claims/dispatches
an existing Attempt; retry creates a new Attempt (`ordinal` increments) over the same frozen input.

Transient retrieval failures MAY retry inside the same Attempt. Once an Attempt is failed, V1 MUST NOT resume that same failed Attempt. A new Attempt may be created according to retry policy.

Failure taxonomy must distinguish permanent Execution/input failures, transient retrieval failures, and Node-local/environment failures.

A Node-local failure such as local disk exhaustion MUST NOT automatically imply that immutable Execution input is invalid.

V1 does not implement durable file-by-file materialization checkpoints. Partial staging/projection state is disposable.

---

# 17. Cloud → Node Skill contract

Keep durable execution identity separate from ephemeral delivery authorization.

## 17.1 SkillBundleRef

```text
SkillBundleRef
├─ skill_revision_id
├─ content_digest
├─ size_bytes
├─ package_format
├─ package_format_version
└─ retrieval
```

## 17.2 RetrievalCapability

```text
RetrievalCapability
├─ method
├─ url
├─ expires_at
└─ optional required headers
```

V1 implementation may use short-lived signed HTTPS GET URLs.

The protocol MUST NOT be named or modeled as S3-specific.

## 17.3 Credential/security classification

A signed URL is a bearer credential.

Therefore full capability URL MUST NOT be stored in PostgreSQL, durably journaled, logged, included in user-facing errors, or returned in durable results/evidence.

It MUST be short-lived, read-only, scoped to the exact immutable object, and grant no list/write/delete rights.

This is an explicit evolution of the existing "business protocol does not receive secrets" model and requires an approved ADR/spec change before implementation.

Do not silently add credentials to an existing protocol.

## 17.4 Capability refresh

Capability expiry MUST NOT change Execution identity.

Refresh conceptually identifies:

```text
execution_id
attempt_id
skill_revision_id
```

Cloud verifies that the Attempt belongs to the Execution, the Execution contains this exact SkillRevision binding, and the Attempt is still eligible for retrieval.

Cloud then issues a new capability for the same immutable object.

Refresh MUST NOT consult `Skill.current_revision_id`, re-resolve AgentSkillBinding, or switch revisions.

Exact route/transport shape must follow existing repository conventions.

---

# 18. Authorization model

Authorization remains a Cloud responsibility.

Requirements:

- Skill ownership is derived server-side from Skill → Collaboration Workspace.
- Never trust client-provided workspace/tenant ownership claims for authorization.
- Cross-Workspace existence must not leak through error differences.
- Execution admission resolves authorization before Skill data reaches Controller/Node.
- Controller and Node are not given tenant/user/member authorization responsibilities.
- Node trusts the already-authorized immutable execution descriptor.
- Capability issuance/refresh is authorized against the durable ExecutionSkillBinding.

Node does not independently re-check Collaboration Workspace membership.

---

# 19. Data plane

Control plane:

```text
Cloud
→ Controller
→ Node
```

Data plane for Skill bytes:

```text
Node
→ Object Storage
```

Controller MUST NOT proxy Skill package bytes.

Cloud API MUST NOT become the normal package-byte proxy.

Long-lived Object Storage credentials MUST NOT be placed in execution business messages.

---

# 20. Node verified content cache

V1 includes a Node-local digest-addressed verified cache.

Semantic identity:

```text
digest_algorithm + content_digest
```

Business ownership MUST NOT be inferred from cache identity.

Different Workspaces/Skills may physically reuse identical verified bytes while remaining separate business resources.

Required behavior:

```text
cache hit
→ verify evidence/content as required
→ use immutable cached content

cache miss
→ acquire capability
→ download to staging
→ verify size
→ verify digest
→ atomic publish to cache
```

Requirements:

- per-digest population concurrency control;
- atomic publish;
- corrupted entries are never treated as valid;
- cache is immutable after verified publish;
- cache is derived/disposable state;
- deleting the cache only causes re-download, not business-state loss.

Cache GC is independent from Execution projection cleanup.

V1 must include a bounded retention mechanism or a clearly safe initial GC policy; do not create an intentionally unbounded permanent cache.

Exact quota/LRU/age policy is an implementation detail unless existing Node conventions dictate it.

---

# 21. Node materialization contract

## 21.1 Separation of states

```text
Object Storage
= authoritative immutable bytes

Node Verified Cache
= verified reusable derived bytes

ExecutionSkillProjection
= Attempt-scoped Agent-visible filesystem
```

## 21.2 AgentRuntimeAdapter

Agent-specific Skill discovery conventions belong to the Node Agent runtime adapter layer.

Conceptual responsibility:

```text
VerifiedSkillBundle[]
→ provider/runtime-specific filesystem/config projection
→ launch specification
```

Cloud/SkillRevision must not encode provider-specific filesystem paths.

## 21.3 Per-Attempt projection

Each Attempt gets its own managed Skill projection.

Agents MUST NOT consume or mutate the shared verified cache directly.

Projection implementation may use copy/reflink/mount/etc., but MUST preserve cache immutability. Writable hardlink semantics that can mutate the shared cache are prohibited.

## 21.4 Projection staging and publish

Never construct required Skill content directly in the final Agent-visible path.

Required semantic sequence:

```text
projection staging
→ write complete tree
→ validate
→ finalize
→ atomic publish
→ projection ready
```

Partial projection MUST NOT be Agent-visible. A stale staging tree after crash is disposable.

## 21.5 Content preservation

Canonical Skill package files should remain byte-preserving in V1.

Do not silently rewrite `SKILL.md` during normal projection.

If a specific Agent runtime requires deterministic transformation, it must be modeled explicitly as an AgentRuntimeAdapter projection transformation and covered by tests/spec evidence.

## 21.6 Ownership

Node may delete/replace only paths it owns for the relevant Attempt projection.

Unknown/user-created files are Preserved, not implicitly overwritten or deleted.

Do not copy Desktop Effect architecture wholesale; only preserve required capabilities such as ownership, evidence, and fail-closed readiness.

---

# 22. Preparation readiness barrier

Attempt preparation has a hard pre-spawn barrier.

Conceptual durable phases may be:

```text
PLANNED
→ PREPARING
→ READY
→ STARTING
→ RUNNING
→ terminal
```

Exact existing state names should be reused where possible. Do not introduce a parallel state machine unnecessarily.

`READY` semantically requires:

- repository/runtime input ready;
- every required SkillRevision resolved;
- every required Skill bundle locally verified;
- every required Agent-native Skill projection atomically published;
- required Agent runtime configuration prepared.

Strong invariant:

> Agent process MUST NOT spawn unless all required Skill materialization is ready.

A required Skill projection failure MUST fail preparation. It MUST NOT be downgraded to warning and continue with stale/missing Skills.

There are no optional Skills in V1 unless this plan is explicitly updated.

READY is an audit/consumption barrier, not permission to resume a failed Attempt after crash.

---

# 23. Script execution boundary

Upload, normalization, validation, hashing, storage, retrieval, caching, and materialization MUST NOT EXECUTE SKILL CONTENT.

Scripts, package manifests, binaries, and WASM are bytes during ingestion/materialization.

Execution occurs only later through the Agent/runtime tool boundary.

The current Node environment does not provide a general security sandbox. Do not claim Skill isolation that does not exist.

Any future security sandbox/containment change requires its own approved architecture decision.

---

# 24. Secret handling

Required checks:

- no signed retrieval capability in DB;
- no capability in logs;
- no capability in telemetry/error strings;
- no capability in fixtures/snapshots;
- no capability in process journal;
- no object-store long-lived credential in business protocol;
- no credential embedded in repository/Skill URLs;
- sanitized errors across Cloud/Controller/Node boundaries.

Tests must cover accidental logging/serialization where practical.

---

# 25. Soft delete and retention

Skill is soft-deleted according to repository conventions.

Soft deletion:

- removes it from ordinary future selection/import matching as appropriate;
- does not invalidate immutable ExecutionSkillBinding rows that already exist;
- does not require immediate deletion of immutable revision objects.

Object retention/garbage collection must be separate from business deletion and must not delete content still required for active/nonterminal Executions, audit/recovery retention requirements, or retained immutable revisions according to policy.

Exact long-term object retention policy may be implemented later if not needed for V1 correctness, but unsafe eager deletion is prohibited.

---

# 26. Idempotency

Creation/import APIs must follow existing `Idempotency-Key` rules.

Important distinction:

```text
Idempotency-Key
!=
content deduplication
```

Same idempotency key + same request replays stored response according to repository rules.

Same idempotency key + different request conflicts.

Different idempotency keys with identical canonical Skill content may produce multiple SkillIngestion attempts, but must converge to the same existing SkillRevision for the same Skill/digest.

External Object Storage mutation must use stable object identity so ambiguous results can be reconciled safely.

---

# 27. Compatibility and ADR requirements

This change introduces capabilities not currently represented in Cloud/Controller/Node specs:

- first-class Cloud Skill;
- immutable SkillRevision package in Object Storage;
- exact SkillRevision execution snapshot;
- Node non-Git artifact retrieval;
- digest verification;
- Node Skill cache;
- Agent runtime Skill materialization;
- preparation READY barrier;
- ephemeral signed retrieval capability/credential transport;
- Execution/Attempt retry semantics as they relate to Skill inputs.

Before or together with implementation, update/add the relevant approved ADRs and core test evidence.

The signed retrieval capability is especially important:

> Existing clone protocol rules say business protocol does not receive secrets. A signed URL is a bearer credential. This plan intentionally introduces a constrained ephemeral retrieval credential, so the architecture/spec must explicitly approve that evolution.

Do not treat this as a backward-compatible implementation detail.

---

# 28. API and contract discipline

When public/internal routes or payloads change:

- update router route definitions;
- update internal contract definitions;
- regenerate OpenAPI;
- regenerate frontend API client;
- do not hand-edit generated artifacts;
- add contract/integration tests.

Strict request decoding remains required.

Server-owned fields such as workspace ownership, revision object locator, digest after canonicalization, and execution binding identity must not be accepted as untrusted client authority.

---

# 29. Migration rules

- Read migration conventions before editing.
- Applied migrations are immutable/checksummed.
- Add forward migrations only.
- PostgreSQL constraints should enforce invariants where practical.
- Test fresh database and upgrade from previous schema.
- Do not run DDL dynamically at server startup.

Likely new constraints include, subject to repository schema conventions:

- active Workspace/canonical-name uniqueness;
- SkillRevision `(skill_id, digest_algorithm, content_digest)` uniqueness;
- foreign-key ownership relationships;
- Attempt numbering/Execution relationship invariants.

Exact table names must follow current domain naming conventions discovered during implementation.

---

# 30. Transaction and external-effect rules

All DB transactions must remain short and database-only.

Never hold transaction, row lock, or advisory lock across Object Storage, HTTP, filesystem, Controller, Node, process, or Git.

Persist stable effect intent/evidence before external mutation when recovery requires it.

Ambiguous external results must be reconciled by stable identity.

Use existing lease/epoch/version fencing rules for stale writers.

---

# 31. Frontend requirements

If the frontend Skills board/import UI is part of this implementation wave:

- read `frontend/AGENTS.md`;
- preserve module documentation rules;
- update Chinese `README.md` and English `README.en.md` for changed/new modules as required;
- add module tests;
- use generated API client;
- do not hand-edit generated API output.

UI semantics must reflect backend truth:

- one target Collaboration Workspace per batch;
- recursive discovery;
- per-candidate result;
- created / updated / unchanged / failed summary;
- display name edits do not mutate package metadata;
- revision/content errors are per candidate.

Do not expose signed retrieval capabilities to the browser unless a separately approved product flow requires it. Node retrieval capabilities are not user-facing download links.

---

# 32. Testing requirements

Tests must cover the actual contract boundaries.

## 32.1 Canonicalization

- directory and archive with identical files → same canonical digest;
- Windows separator normalization;
- traversal rejection;
- absolute path rejection;
- duplicate normalized path rejection;
- case-insensitive collision rejection;
- nested Skill root rejection;
- symlink/special-node rejection;
- UTF-8 requirement for `SKILL.md`;
- binary supporting file preservation;
- file/size/decompression limits;
- archive-bomb limit behavior.

## 32.2 Skill identity/revisions

- same Workspace + same canonical name → same Skill;
- explicit target_skill_id update semantics;
- display_name does not affect matching;
- same Skill + same digest → existing revision reused;
- older digest re-import → existing old revision reused;
- different Skills + same digest → distinct revision rows/business identity;
- concurrent same-content ingestion converges under DB constraints/idempotency.

## 32.3 Ingestion/recovery

- object upload succeeds then DB finalization fails;
- ambiguous object-store result reconciles by stable identity;
- activation CAS conflict leaves valid revision without corrupting current pointer;
- batch partial success;
- failed candidate does not roll back siblings.

## 32.4 Authorization

- cross-Workspace Skill lookup/import/update does not leak existence;
- Execution snapshot authorization occurs in Cloud;
- capability cannot be minted/refreshed for revision not bound to Execution;
- expired capability refresh preserves exact revision;
- deleted/updated current Skill does not alter existing Execution binding.

## 32.5 Execution snapshot

- Agent binding changes after Execution creation do not affect Execution;
- Skill current revision changes after Execution creation do not affect Execution;
- Retry/new Attempt uses same ExecutionSkillBinding;
- Run Again/new Execution resolves current configuration.

## 32.6 Node retrieval/cache

- cache miss → download → verify → publish;
- cache hit avoids retrieval;
- wrong size fails;
- wrong digest fails;
- corrupt cache is not consumed;
- concurrent population is safe;
- capability expiry/refresh retries same revision;
- credential is not persisted/logged;
- cache deletion causes safe re-download.

## 32.7 Projection/readiness

- projection built in staging;
- partial staging is never Agent-visible;
- AgentRuntimeAdapter maps to correct native layout;
- required Skill failure prevents spawn;
- READY required before spawn;
- crash/stale staging rebuild is deterministic;
- cache cannot be mutated via writable projection;
- unknown/user files are not overwritten/deleted.

## 32.8 Race/concurrency

Because this work changes recovery, shared cache, persistence lifecycle, and concurrent ingestion/materialization, run the repository race gate required by `AGENTS.md`.

---

# 33. Non-goals for V1

Unless this plan is explicitly updated, V1 does NOT include:

- global Skills independent of Collaboration Workspace;
- personal/user-only Skills;
- moving a Skill between Workspaces by update;
- per-run temporary Skill add/remove overrides;
- nested Skill packages;
- symlink support;
- executing Skill scripts during ingestion/materialization;
- a general Node security sandbox;
- file-level resumable materialization;
- resumable partial package download;
- mutable SkillRevision;
- Node resolving `current_revision`;
- Controller proxying Skill package bytes;
- long-lived Object Storage credentials in business protocol;
- copying Desktop Effect architecture into Node wholesale;
- treating Workflow as an actor.

---

# 34. Implementation sequencing

## Phase 1 — Specs / ADR closure

Before implementation of new semantics:

1. Read `specs/AGENTS.md`.
2. Add/update ADRs for:
   - Cloud Skill ownership/storage/revisions;
   - execution immutable SkillRevision snapshot;
   - Object Storage retrieval capability;
   - credential handling exception/evolution;
   - Node verified bundle/cache/materialization;
   - preparation READY barrier and Attempt recovery semantics.
3. Add/update core test cases for the invariants in this plan.
4. Keep status/evidence markers accurate.

If approved existing ADRs conflict, STOP and report.

## Phase 2 — Cloud persistence/domain

- migrations;
- Skill;
- SkillRevision;
- AgentSkillBinding;
- ExecutionSkillBinding;
- SkillIngestion;
- constraints/indexes;
- persistence tests.

## Phase 3 — Canonical ingestion + Object Storage

- directory/archive adapters;
- virtual tree;
- discovery;
- validation;
- canonical manifest/package;
- digest;
- object-store port/adapter;
- journal-first ingestion saga;
- partial-success batch behavior.

### Step 2B Object Storage abstraction — port / semantic types / reconciliation core IMPLEMENTED; saga now drives the port

Status: **the frozen abstraction is implemented as code** — the `ObjectStore` port, semantic types
(`PutRequest`/`PutResult`/`StatResult`/`GetResult`, `Locator`, `Verdict`), the provider-neutral
`Reconcile` core, and the `internal/skillstore/fakestore` test double, all in `internal/skillstore`
(+ subpackage). The `internal/core` ingestion saga (Step 2C, below) now drives `PutImmutable`/`Reconcile`
through `Store.SkillsObjectStore`. There is still **no** production Object Storage provider, SDK dependency,
configuration key, upload HTTP API, `SKILL.md` discovery, or `RetrievalCapability`, and `0015`
needs no change. Frozen by `specs/decisions/cloud/skills/20260927-object-storage-abstraction.md`
(status `proposed`). This subsection is the acceptance contract for the Object Storage work in this
phase. Step 2B implemented **only** the write-and-verify abstraction layer and its tests; it did not
mark the ingestion pipeline, provider integration, or Node delivery implemented (the pipeline/saga is
implemented in Step 2C below; provider integration and Node delivery remain unimplemented).

The acceptance gates below are the full-phase gates; the abstraction-only slice satisfies the pure-semantic
ones (key derivable with no external I/O, no overwrite path, no `error != nil` retry decision, `DEFINITE_FAILURE`
only on provable no-effect, `MISMATCH` fail-closed, test double expressiveness) and defers to later steps the
pipeline/DB-orchestration ones (ingestion state transition, DB TX #2, crash adoption, credential redaction).

**ObjectStore semantic contract.** Three operations only: `PutImmutable(request)`, `Stat(locator)`,
`Get(locator, bound)`. The request carries the full immutable expectation set — stable locator,
expected package digest, expected package size, expected content digest, package format, package
format version, digest algorithm — and each operation's result type must express the whole five-value
taxonomy below; a bare `(value, error)` is not acceptable. No `List`, `Delete`, `Copy`, `Move`, or
`Presign` in this slice. No provider-specific naming (`S3*`, `Bucket*`, `AWS*`). Bucket/region/
credentials come from the injected adapter configuration, never from call arguments or persisted
data. The port is defined at the consuming boundary and implemented privately under `internal/`; the
provider client is constructed in `cmd/*` wiring and injected, so domain code never reads provider
configuration. `PutImmutable` is create-only: a provider without conditional-write support must fail
closed rather than degrade to unconditional overwrite. Its `created` and `already-exists` outcomes are
both **non-verifying** — `already-exists` means "this key is occupied", not "the content matches" —
and only the three verification checks can produce a verified result. Provider `ETag`/checksum/length
is evidence only, never identity.

**Identity model.** Three layers stay separate: business identity = `SkillRevision.id`; content
identity = `(digest_algorithm, content_digest)` (the Step 2A tree digest); physical identity = the
object key. `content_digest` is the tree digest and is NOT `SHA256(package bytes)` — the two must
never be interchanged. Content identity drives business dedup
(`UNIQUE(skill_id, digest_algorithm, content_digest)`); physical identity drives physical dedup.
`size_bytes` / `file_count` describe the canonical tree, not the container.

**Package checksum decision.** `package_digest = SHA256(exact ora-skill-package v1 bytes)`, lowercase
hex. It is used only to (a) derive the object key, (b) reconcile ambiguous writes, and (c) detect
byte-level corruption. It is NOT business identity, NOT the revision dedup key, and NOT the Node
cache key (that stays `digest_algorithm + content_digest`). It needs no new column because the object
key embeds it.

**Object key decision.**
`skills/<package_format>/v<package_format_version>/<digest_algorithm>/<package_digest_hex>`
(e.g. `skills/ora-skill-package/v1/sha256/…` — 99 bytes today, ≤ 277 bytes worst case under the
`0015` column bounds, so the existing `<= 1024` CHECK holds). The key must be computable before any
DB transaction or external call; `skillpkg.Build` is a deterministic pure function, so it is. The key
carries no tenant/workspace/skill/revision/ingestion identity, no timestamp and no random value.
`PutImmutable` is create-only: a provider without conditional-write support must fail closed rather
than degrade to unconditional overwrite. Provider `ETag`/checksum/length is evidence only, never
identity, and can never by itself produce a verified result.

**Reconciliation algorithm.** An ambiguous outcome is never guessed, always probed. Probe the
persisted `object_locator` (never a re-derived key): `Stat` → `absent` ⇒ `CONFIRMED_ABSENT`; present
⇒ `Get` with a bounded read, then require all three of `SHA256(bytes) == package_digest`, successful
`skillpkg.Decode`, and `TreeDigestHex() == content_digest` ⇒ `CONFIRMED_PRESENT_MATCHING`; any
mismatch ⇒ `MISMATCH`; error ⇒ `INDETERMINATE` with a bounded probe retry budget. Only
`CONFIRMED_PRESENT_MATCHING` may enter DB TX #2.

**Failure taxonomy.** Five determinate-or-ambiguous outcomes, never `error != nil`:
`CONFIRMED_PRESENT_MATCHING` / `CONFIRMED_ABSENT` / `MISMATCH` / `DEFINITE_FAILURE` /
`INDETERMINATE`. `DEFINITE_FAILURE` requires proof that the request had no effect (pre-acceptance
rejection: DNS, connection refused, 429, auth/validation rejection); anything where the request may
have been processed is `INDETERMINATE`. `MISMATCH` is determinate and fails closed: never overwrite,
never delete-and-rewrite, never adopt, never switch keys. `INDETERMINATE` is the only ambiguity and
stays recoverable. A PUT may be retried only after `ABSENT`, or after a transient `DEFINITE_FAILURE`;
never after `INDETERMINATE` without a probe, and never after `MISMATCH`. The retry unit is the
ingestion, not the HTTP request. A crash after a successful PUT but before DB TX #2 must adopt the
existing correct object — the external object is content-addressed and immutable, so adoption is safe
and a re-PUT is not. `created` and `already-exists` are both non-verifying PUT outcomes: only the
three verification checks can produce `CONFIRMED_PRESENT_MATCHING`.

**Transaction boundary.** Unchanged from §13: `skillpkg.Build` runs outside any transaction; DB TX #1
commits the ingestion intent plus the stable `object_locator` before any external mutation; all
Object Storage I/O runs outside any transaction, row lock, or advisory lock; DB TX #2 runs only after
`CONFIRMED_PRESENT_MATCHING`. `skill_ingestions.state` maps as `planned` (post-TX #1, pre-PUT) →
`storing` (PUT issued or outcome unconverged, i.e. reconcile-required — sourced from every
`INDETERMINATE`, and from a transient `DEFINITE_FAILURE` while retry budget remains) → `verified`
(proved `MATCHING`) → `committed` (a durable `SkillRevision` exists) / `failed` (determinate
permanent failure or `MISMATCH`; a transient `DEFINITE_FAILURE` whose budget is exhausted also lands
here, with a transient-flavoured `error_code`). Ambiguous results are never recorded as `failed`, and
`committed` is never recorded without a durable revision.

**Migration requirement.** NONE for this design. `internal/core/migrations/0015_skills.sql` must not
be modified; its `object_locator` columns already fit the derived key. Any later need for an indexed
or checked `package_digest` column, or for concurrency fencing beyond
`UPDATE … WHERE state = <expected>`, requires a NEW forward migration (`0016_*` or later).

**Implementation acceptance gates** (this phase is complete only when all hold):

- [ ] no Object Storage call executes inside a DB transaction, row lock, or advisory lock;
- [ ] the object key is derivable with no external I/O and contains no business identity;
- [ ] no code path can overwrite an existing object; a provider lacking conditional writes is refused;
- [ ] `CONFIRMED_PRESENT_MATCHING` requires all three verification checks; no provider evidence alone
      can produce it;
- [ ] retry/failure decisions are driven by the outcome enum; no code path uses `error != nil` to
      decide whether to retry;
- [ ] `DEFINITE_FAILURE` is only produced when the request is provably without effect; anything that
      may have been processed is classified `INDETERMINATE`;
- [ ] test doubles can express create-only, `already-exists`, `definite-failure` and `ambiguous`;
      a fake that cannot is not admissible evidence for D7/D10/D11;
- [ ] an ambiguous result leaves the ingestion in `storing`, never `failed`;
- [ ] `MISMATCH` fails closed: no revision created, `Skill.current_revision_id` unchanged;
- [ ] a crash between a successful PUT and DB TX #2 is recovered by adopting the existing object;
- [ ] provider details, credentials, and `object_locator` never reach user-facing errors;
- [ ] `0015_skills.sql` is byte-identical to its Step 1A state; no migration was added for this design.

### Step 2C Canonical ingestion saga — IMPLEMENTED

Status: **implemented**. The candidate model, idempotency, transaction boundaries, and crash-recovery semantics
frozen by `specs/decisions/cloud/skills/20260927-canonical-ingestion-saga.md` (status `proposed`) are now code:
`internal/skillmeta` (the `SKILL.md` metadata parser), the forward migration
`0016_skill_ingestion_idempotency.sql` (four additive `skill_ingestions` changes), and the journal-first saga
`Store.IngestSkill` / `Store.IngestSkills` in `internal/core` (driving the `internal/skillstore` port through
`Store.SkillsObjectStore`), with 11 real-PostgreSQL integration tests in `integration/skill_ingestion_test.go`.
There is still **no** upload HTTP API, production Object Storage provider, or `RetrievalCapability`;
`0015_skills.sql` is byte-identical to its Step 1A state. The directory/archive source adapter and recursive
`SKILL.md` discovery are delivered in Step 2C.1 (`internal/skillsource` + `Store.IngestSource`).
This subsection is the acceptance contract for the Step 2C implementation slice.

**SKILL.md metadata contract — implemented (`internal/skillmeta`).** The `canonical_name` derivation gap was
closed by `specs/decisions/cloud/skills/20260927-skill-md-metadata-contract.md` (status `proposed`), and the
parser is now implemented in `internal/skillmeta`: `canonical_name = ASCII lowercase(TrimSpace(name))` is the
single transformation; `name` is required and must be a YAML string matching ASCII `[A-Za-z0-9._-]+` (not
starting with `.`, ≤ 200 bytes); `description` is optional (string, ≤ 4096 bytes); duplicate keys and invalid
YAML fail closed; unknown fields are opaque and preserved; `internal/skillpkg` stays frozen and does not parse
`name`. Compatible with Desktop `0-static-skill-package.md` D3 and with all audited multica content.

**Candidate model.** A directory/archive source is normalized in pure CPU to a deterministically ordered
candidate list (recursive `SKILL.md` discovery; each direct parent = one candidate root; ordered by canonical
path). Nested `SKILL.md` roots are invalid and fail closed. A batch is partial-success
(DISCOVER → VALIDATE → APPLY EACH → SUMMARY); one candidate's failure does not roll back siblings; each
candidate produces exactly one `skill_ingestions` row (its durable saga journal). Batch matching key is
`(workspace_id, canonical_name)`; an explicit update uses `target_skill_id` as authoritative; there is no
rename inference.

**Idempotency.** The request-level `Idempotency-Key` is distinct from content identity (root D20). The
per-candidate idempotent identity is `(workspace_id, idempotency_key, canonical_name)` (the **locator**); the
semantic request fingerprint is a domain-separated, length-prefixed SHA-256 hex over
`(target_skill_id, display_name, summary, content_digest, package_digest)` (the **equality key**) — never the
raw upload bytes, never the key, and never `(content_digest, package_digest)` alone (the same package can serve
different business requests: a different `target_skill_id`, or caller-chosen `display_name`/`summary`, changes
final state). Transport-only fields (multipart boundary, raw zip/tar byte order, compression metadata, mtime,
HTTP header order, transport filename) are excluded; a directory source and an archive source with the same
canonical content and same business semantics converge to the same fingerprint. `same key + same fingerprint →
resume/replay`; `same key + different fingerprint → conflict`; `different keys + same content → multiple
ingestions, one revision, one object`.

**Recovery model = CLIENT-DRIVEN CONTINUATION (the Step 2C.0 core).** The Step 2C.0 design separates
*durable state recovery* (provided: ingestion identity/state/idempotent identity/activation outcome are all in
the DB) from *durable payload recovery* (NOT provided: the canonical package bytes are not server-recoverable).
V1 has **no** server-side background recovery worker (Step 2B.0 D18: the `Idempotency-Key` converges same-key
requests at the HTTP layer) — the saga is synchronous and client-driven, so **Step 2C does not provide
autonomous package-byte recovery before canonical Object Storage persistence**. Because `skillpkg.Build` is a
deterministic pure function (Step 2B.0 D5), crash recovery is: the client re-submits the same
`Idempotency-Key` + same source; the new request **deterministically rebuilds** the exact canonical package
bytes, matches the durable identity (`object_locator` + `expected_digest` + `request_fingerprint`) in
`skill_ingestions`, and resumes — probe → `ABSENT` ⇒ PUT (with the freshly rebuilt bytes), `MATCHING` ⇒ adopt
straight into DB TX #2. There is no recovery root that depends on the original in-flight request, a
server-spooled temp dir, or an in-process byte slice. This keeps root D19 / Step 2B.0 invariant 19 (TX #1
before any external mutation) intact: the bytes are *rebuilt*, never persisted server-side, so no staging
store, no PUT-before-journal, and no source blob are needed.

**Stranded ingestion semantics.** If the client never re-submits, a `planned`/`storing` ingestion strands
indefinitely; the server will not (and cannot — no payload, no worker) autonomously advance it. Resumption is
possible only through the **same** `Idempotency-Key` (a new key = a new ingestion); V1 has no automatic
cleanup (left to the object retention/GC decision), and stranded rows must be observable (list by
`workspace_id` + `state IN ('planned','storing')`) without inferring external object state.

**Transaction boundary.** Source read / archive parse / `SKILL.md` discovery are I/O that runs outside any
transaction; `skillpkg.Build` is deterministic content processing (pure CPU). DB TX #1 (short, DB-only)
authorizes, binds the idempotency key, and inserts `skill_ingestions(state='planned', canonical_name,
idempotency_key, request_fingerprint, source_type, expected_digest=content_digest, digest_algorithm,
object_locator=key)` — no package bytes, no Object Storage I/O. The external stage (`PutImmutable` → probe →
D9 verify) runs entirely outside any transaction/lock. DB TX #2 (short, DB-only, only after
`CONFIRMED_PRESENT_MATCHING`) creates or reuses the Skill and SkillRevision (reuse via
`UNIQUE(skill_id,digest_algorithm,content_digest)`), backfills `target_skill_id` for new Skills, finalizes
`state='committed'`, writes `activation_outcome` (`'activated'` on CAS success, `'activation_conflict'` on a
version conflict), and CAS-advances `Skill.current_revision_id` on `Skill.version`. A CAS conflict leaves the
revision valid, the pointer untouched, and the ingestion `committed`, with the activation outcome recorded
durably for replay.

**Migration requirement.** `0015_skills.sql` must not be modified — and is not. The design added **one new
forward migration** (`0016_skill_ingestion_idempotency.sql`, applied) with four additive changes:
`ALTER TABLE skill_ingestions ADD COLUMN canonical_name text` (nullable, with a
`IS NULL OR length(...) BETWEEN 1 AND 200` CHECK so the Step 1A ingestion test that does not set it still
passes);
`ALTER TABLE skill_ingestions ADD COLUMN request_fingerprint text`
(`IS NULL OR length(...) BETWEEN 1 AND 128` CHECK);
`ALTER TABLE skill_ingestions ADD COLUMN activation_outcome text`
(`IS NULL OR activation_outcome IN ('activated','activation_conflict')` CHECK); and
`CREATE UNIQUE INDEX skill_ingestion_candidate_uniq ON skill_ingestions(workspace_id, idempotency_key,
canonical_name)`. No `package_digest` / `version` / `size_bytes` / `file_count` columns are added, and no
`display_name`/`summary` columns (those are encoded in `request_fingerprint`).

**Implementation acceptance gates** (Step 2C implementation is complete only when all hold):

- [x] each candidate has an independent, durable, replayable `skill_ingestions` identity
      (`workspace_id, idempotency_key, canonical_name` unique);
- [x] the semantic request fingerprint is `(target_skill_id, display_name, summary, content_digest,
      package_digest)`; a same-key resubmission with a different fingerprint is a conflict, not a replay;
- [x] crash recovery deterministically rebuilds the canonical package bytes from a same-key resubmission and
      verifies them against the durable identity (`object_locator` + `expected_digest` + `request_fingerprint`)
      — no path re-reads an ephemeral upload or an in-process slice;
- [x] a crash between TX #1 and PUT resolves by probe: `ABSENT` ⇒ PUT (rebuilt bytes), `MATCHING` ⇒ adopt;
- [x] a crash after a successful PUT but before TX #2 adopts the existing object, never re-PUTs;
- [x] no Object Storage / filesystem / HTTP / process work runs inside a DB transaction, row lock, or
      advisory lock (source read/archive parse/discovery are outside transactions; `skillpkg.Build` is pure CPU);
- [x] `MISMATCH` fails closed (no revision, `current_revision_id` unchanged, stable error code);
- [x] activation uses `Skill.version` CAS; a conflict preserves the immutable revision and the untouched
      pointer, writes `activation_outcome='activation_conflict'` durably, and does not re-attempt the CAS on
      replay (new business attempt = new `Idempotency-Key`);
- [x] a `planned`/`storing` ingestion never advances autonomously (no worker) — it strands until the same
      `Idempotency-Key` re-submits;
- [x] `0016_skill_ingestion_idempotency.sql` is applied (all four changes); `0015_skills.sql` is byte-identical
      to Step 1A.

### Step 2C.1 Source intake & candidate discovery — IMPLEMENTED

Status: **implemented** (`internal/skillsource` + `core.Store.IngestSource` seam). The four source-intake
semantics closed by `specs/decisions/cloud/skills/20260927-source-intake-candidate-discovery.md`
(status `proposed`, approved for implementation) are realized in code; no migration was written.

- [x] **Archive support set** = ZIP + uncompressed TAR only (`.zip` / `.tar`); `.tar.gz` / `.tgz` / gzip are
      unsupported (no sniffing, no parser fallback). Archive encoding is caller-supplied `SourceKind ∈
      {zip, tar}` — never filename/MIME/sniffed.
- [x] **Files outside candidate roots** are validated for safety first, then ignored for candidate
      construction: safe-but-unattached → ignored; unsafe-unattached → reject the whole source. A source
      root with its own `SKILL.md` is the single root candidate (owns the whole tree).
- [x] **Nested candidate roots** are a source-level structural error (`ErrNestedRoot`): the whole source is
      rejected before any candidate enters the saga. Nested-ness is judged by canonical root-path components
      (`a`/`a/b` nested; `a`/`ab` not).
- [x] **Duplicate `canonical_name` within one source** is a source-level structural conflict
      (`ErrDuplicateName`): the whole source is rejected before TX #1 — never first/last wins, never DB
      `UNIQUE` decides.
- [x] **Failure boundary**: source-level structural failures → zero `SkillIngestion` rows; invalid `SKILL.md`
      metadata → candidate-local `PreparationFailure{CandidateRoot, ErrorCode, Detail}` (no ingestion row,
      siblings continue, no synthetic `canonical_name`), carried by `PreparedSourceResult{Candidates[],
      PreparationFailures[]}`; only a resolved `PreparedCandidate[]` enters the existing `IngestSkills` saga
      (partial-success unchanged), via `core.Store.IngestSource`.
- [x] **SourceType**: directory → `"directory"`, zip/tar → `"archive"`; directory / ZIP / TAR of the same
      logical tree converge on identical candidate paths, bytes, `canonical_name`, manifest, content digest,
      package bytes, and package digest.
- [x] **Schema impact**: NONE — `0015_skills.sql` and `0016_skill_ingestion_idempotency.sql` are unchanged; no
      `0017`.
- [ ] Public upload HTTP API / production Object Storage provider / frontend / RetrievalCapability / Node
      delivery / Agent-Execution bindings remain NOT implemented (later phases).

### Step 3A Public upload API contract — FROZEN (design only)

Status: **API contract frozen**; implementation **NOT started**. Frozen by
`specs/decisions/cloud/skills/20260927-public-upload-api-contract.md` (status `proposed`, design-only).

- [x] **Endpoint**: `POST /api/v1/tenants/:tid/spaces/:spaceId/skills/imports` — tenant-prefixed and
      space-scoped per repo convention (`spaceId` == the saga's `workspace_id`, i.e. `collab_workspaces.id`);
      not the tenant-less `/api/v1/spaces/{workspace_id}/...` form.
- [x] **Auth**: reuses Collaboration Workspace dual credentials + `workspaceRole`; import / explicit update
      requires space owner/admin (`403 workspace_admin_required`); cross-workspace `target_skill_id` and
      non-member space reads are `404` (no leak).
- [x] **Transport**: `multipart/form-data` with a single archive part. `source_kind ∈ {zip, tar}` (explicit,
      no filename/MIME/sniffing/fallback); `directory` is not a public wire kind — a client packs a local
      directory to zip/tar first (same canonical content; `KindDirectory`/`PrepareDirectory` stay server-side).
- [x] **Request fields**: `source_kind` + `source` (archive bytes) + optional `target_skill_id` (explicit
      update, single-candidate source) + optional `display_name`/`summary` (new-Skill metadata). Clients must
      not send `canonical_name`/`content_digest`/`package_digest`/`object_locator`/`revision_id`.
- [x] **Idempotency-Key**: required header, == the saga's idempotency namespace
      `(workspace_id, idempotency_key, canonical_name)` + `request_fingerprint`; the upload endpoint does NOT
      write the generic `idempotency_records` row (binary body + continuation semantics).
- [x] **Response**: `200` envelope `{sourceKind, preparationFailures[], ingestions[]}`. Source-level structural
      failure → `400` + `source_*` code (zero ingestion rows); candidate preparation failure → per-item without
      any Skill identity; candidate saga failure / `activation_conflict` → per-item inside the `200` envelope.
- [x] **Partial success**: overall `200` whenever the source was structurally valid; only catastrophic DB loss
      → `500`.
- [x] **Replay / continuation**: same key + same fingerprint → durable per-candidate result (`replayed`) or
      `planned`/`storing` resume (client-driven, saga D14); same key + different fingerprint → `409`.
- [x] **Limits**: upload budget ≤ 2 GiB (provisional product cap 256 MiB); archive/entry/expanded/candidate
      limits inherit `skillsource.Limits`; per-file/path limits inherit `skillpkg.Limits`; buffering is
      ephemeral-only (no durable staging). `413 upload_too_large` on budget breach.
- [x] **Schema impact**: NONE — `0015_skills.sql` / `0016_skill_ingestion_idempotency.sql` unchanged; no `0017`.
- [x] Implementation (multipart transport + router/gateway body budget + contract/OpenAPI/frontend sync per
      ADR D17) is complete — see Step 3B below.

### Step 3B Public upload API — IMPLEMENTED

Status: **implemented**. The frozen Step 3A contract is realized end-to-end: multipart transport, route-local
body budget, dual-credential authorization, saga idempotency, the `200` envelope, and the
contract/OpenAPI/frontend sync — with **no schema change**.

- [x] **Route + transport**: `POST /api/v1/tenants/:tid/spaces/:spaceId/skills/imports` registered in
      `router.Routes()` and handled by a dedicated `uploadSkillSource` (`internal/api/router/skill_upload.go`);
      `multipart/form-data` with a single `source` file part and a strict text-field allowlist
      (`source_kind`/`target_skill_id`/`display_name`/`summary`). Unknown or duplicate fields — including the
      forbidden `canonical_name`/`content_digest`/`package_digest`/`object_locator`/`revision_id` — are rejected
      (`unknown_field`/`duplicate_field`); `source_kind` is explicit and maps to `skillsource.SourceKind`, never
      filename/MIME/sniffed.
- [x] **Body budget**: route-local 256 MiB ceiling (`skillsUploadBudget`, `413 upload_too_large`), ephemeral-only
      (no durable staging, never read the full oversized body into memory). Every other route keeps the 64 KiB JSON
      ceiling; the Gateway is route-aware (`requestBodyLimit`/`isSkillsUploadPath` exempt only the 8-segment
      `skills/imports` shape) and the global `maxBodyBytes` is never widened.
- [x] **Authorization**: dual credentials (gateway service role + `X-Ora-User-Token` with `user.Caller ==
      service.Subject`) → `store.ResolveIdentity` → `core.Store.IngestSource`. Whole-request checks fail fast with
      zero side effects: non-member → `404 not_found`; member but not owner/admin → `403 workspace_admin_required`;
      `target_skill_id` resolving outside the space or absent → `404 not_found` (no leak); `target_skill_id` with
      more than one candidate → `400 single_candidate_required`. These are HTTP-level rejections, never per-item
      `errorCode`s inside a `200` envelope.
- [x] **Idempotency**: `Idempotency-Key` required; == the saga namespace. Same key + same fingerprint → replay /
      continuation; same key + different fingerprint → `409 idempotency_conflict`. `IngestSkills` now propagates
      `*Fault`s (auth/conflict/scope) instead of folding them into per-candidate `errorCode`s.
- [x] **Envelope**: `200 {sourceKind, preparationFailures[], ingestions[]}`; source structural failure → `400
      source_*` (zero rows); partial success → overall `200`; `activation_conflict` stays the `activation` field,
      never an `errorCode`.
- [x] **Contract sync**: `internal/contract/openapi.go` (`SourceUploadResult`/`SourcePreparationFailure`/
      `SourceIngestionItem` schemas, multipart request body, `413` description); `api/openapi.json` regenerated
      (`go run ./cmd/openapi`) and gated by `TestPublishedOpenAPIIsValidAndCurrent`; frontend regenerated (orval)
      with `sourceKind`/`preparationFailures`/`ingestions` types — no Skills board UI.
- [x] **Schema impact**: NONE — `0015_skills.sql` / `0016_skill_ingestion_idempotency.sql` unchanged; no `0017`.
- [x] **Tests**: router multipart-strictness/body-budget unit tests (`internal/api/router/skill_upload_test.go`);
      Gateway body-limit pinning unit tests (`internal/gateway/bodylimit_test.go`); E2E
      `integration/skill_upload_test.go` (zip/tar happy path, replay, `409`, member `403` / non-member `404` /
      cross-workspace `404`, `single_candidate_required`, partial-success mapping, fatal-source zero side effects,
      >64 KiB acceptance).
- [ ] Production Object Storage provider / `RetrievalCapability` / Node delivery / Skills board UI /
      AgentSkillBinding / ExecutionSkillBinding remain NOT implemented (later phases).

### Step 4A Production Object Storage provider — FROZEN (design only)

Status: **provider contract frozen; production provider implementation NOT started**. The production
Object Storage gap is closed at the design level by ADR
`specs/decisions/cloud/skills/20260927-production-object-storage-provider.md` (status `proposed`). The
audit found **no** existing authoritative object-storage provider or deployment convention anywhere in the
repo (no S3/AWS/MinIO/GCS/Azure SDK in `go.mod`; `compose.yaml`/CI run only PostgreSQL; no Helm/K8s; no
`storage` config section; no storage references in `scripts/`/`Learn/`/`configs/`/`Taskfile.yml`), so a V1
provider contract is chosen — **single S3-compatible provider, not AWS by default** — with one production
implementation (`aws-sdk-go-v2` + `service/s3`, dependency added only in the implementation slice).

- [x] **Provider**: single S3-compatible contract via custom `endpoint` + path-style; one production adapter.
- [x] **`PutImmutable` create-only**: native conditional `PutObject(IfNoneMatch="*")` → `412` =
      `PutAlreadyExists`; HEAD-then-unconditional-PUT (TOCTOU) is forbidden; no conditional-write → fail closed.
- [x] **Multipart**: V1 uses a **single atomic PUT**, no multipart (preserves create-only atomicity; 5 GiB
      single-PUT ceiling ≫ `MaxPackageBytes` ≈1 GiB and the 256 MiB product cap).
- [x] **Memory**: current `ObjectStore` port keeps `[]byte` full buffering; the single PUT writes the same
      bytes with no amplification — 256 MiB is not operationally unsafe.
- [x] **Error mapping/taxonomy**: 200→`PutCreated`; 412→`PutAlreadyExists`; 400/403/size-reject→
      `PutDefiniteFailurePermanent`; DNS/conn-refused/429→`PutDefiniteFailureTransient`; 5xx/post-send
      timeout→`PutAmbiguous`. Frozen classes: not_found / already_exists / temporary / throttled /
      unauthorized / forbidden / invalid_configuration / integrity_mismatch / ambiguous / permanent_failure;
      raw SDK errors never leak to the domain.
- [x] **Timeouts**: connect 5s / request 30s; every call carries caller context; no unbounded
      `context.Background()`; no background retry worker.
- [x] **Config**: new `storage` section (provider/bucket/region/endpoint/path_style/credential_mode/
      credentials_file/tls.verify/ca_file/allow_insecure_http/timeouts). Bucket is single-configured; bucket
      name never in durable state / request / workspace settings.
- [x] **Credentials**: deployment-platform mechanisms only (environment / workload identity / shared
      credentials file); forbidden in DB / `SkillRevision` / API response / logs.
- [x] **TLS**: production HTTPS + cert verification (`tls.verify` default true); `allow_insecure_http`
      default false (explicit dev/test `http://minio` only).
- [x] **Integrity**: `SHA256 == package_digest` + `skillpkg.Decode` + `TreeDigestHex == content_digest`;
      provider ETag is evidence, never authority.
- [x] **Startup**: `storage` present-but-invalid → fail fast; absent → process starts, `SkillsObjectStore`
      nil, saga returns `object_store_unavailable` (already implemented); no remote bucket probe at startup.
- [x] **Schema impact**: NONE — `0015_skills.sql` / `0016_skill_ingestion_idempotency.sql` unchanged.
- [ ] Production provider **implementation** (adapter + SDK dep + `cmd/server` wiring + `CLOUD_STORAGE_*`
      binding + startup validation) is NOT started (later slice).

### Step 4B Production Object Storage provider — IMPLEMENTED

Status: **implemented and gated; all changes remain uncommitted**. The Step 4A contract is now a production
adapter — new package `internal/skillstore/s3store`, an `internal/config` `storage` section, and `cmd/server`
wiring — with **no change** to the `ObjectStore` port, the schema, or the ingestion saga semantics.

- [x] **Adapter** (`internal/skillstore/s3store`): implements `skillstore.ObjectStore` over
      `aws-sdk-go-v2/service/s3`; a minimal unexported `api` seam (`*s3.Client` satisfies it) keeps tests
      provider-free. `PutImmutable` issues exactly one `PutObject` with `IfNoneMatch: "*"` (no HEAD-then-PUT);
      `Stat` = HEAD; `Get` = bounded read with a +1-byte oversize peek (never `io.ReadAll` unbounded).
- [x] **Error classification**: `classify` keys off the HTTP status via `*smithyhttp.ResponseError`
      (reachable for every non-2xx through `*smithy.OperationError`), then DNS / `dial`-refused → transient,
      established-then-broken / deadline / cancel → ambiguous, unknown → ambiguous. 404→absent, 412→
      already-exists, 429→transient, 5xx→ambiguous, other 4xx→permanent. Raw SDK errors never cross the port.
- [x] **Timeout/transport**: connect 5s / request 30s (frozen defaults, configurable); each op derives its
      deadline from the caller context; transport is `http.DefaultTransport.Clone()` (no global mutation);
      `RetryMaxAttempts: 3` (bounded transport retry only).
- [x] **Credentials**: `environment` (static from `AWS_*`), `shared_credentials_file`, `workload_identity`
      (default chain) — resolved in-adapter, never persisted or logged.
- [x] **Config** (`internal/config` `storage` section + `CLOUD_STORAGE_*`): pointer `StorageConfig` (nil =
      absent → `SkillsObjectStore` nil); `applyDefaults` (credential_mode→environment, tls.verify→true,
      timeouts→5s/30s) then `Validate` (provider=="s3", bucket/region required, endpoint scheme + http-vs-
      allow_insecure_http, credential_mode set, credentials_file required, verify=false ⊥ ca_file, timeouts
      ≥0). `configs/config.yaml` carries a commented sample.
- [x] **Wiring**: `cmd/server.wireObjectStore` translates `storage` → `s3store.Config` and assigns
      `store.SkillsObjectStore`; present-but-invalid fails startup, absent → nil (unchanged
      `object_store_unavailable` path).
- [x] **Tests**: `s3store_test.go` (fake-`api` outcome matrix, `If-None-Match: *`/body passthrough, bounded
      Get, `New` validation) + a real-SDK `httptest` end-to-end (conditional PUT, 412→`PutAlreadyExists`);
      `internal/config/storage_test.go` (nil-when-absent, defaults, full parse, reject matrix, http-with-
      allow, `CLOUD_STORAGE_BUCKET` env). Gates: `go build`/`go vet`/`go test ./internal/... ./cmd/...`/
      `golangci-lint`/`git diff --check` all pass (only pre-existing `cmd/devsetup` Windows file-mode
      failure, unrelated).
- [ ] **Not done**: `RetrievalCapability`, Node delivery, Skills board, Agent/Execution binding, GC/retention,
      MinIO dev fixture, real-provider CI — all later slices.

## Phase 4 — Public API / Skills board backend

- Workspace-scoped CRUD/import;
- revision activation/update semantics;
- Agent Skill assignment;
- idempotency/version behavior;
- generated contracts/OpenAPI/client.

### Step 4A Agent/AgentSkillBinding contract — FROZEN (design only)

Status: **contract frozen; implementation NOT started; all changes remain uncommitted**. The minimal durable
`Agent` + `AgentSkillBinding` authority model is closed by
`specs/decisions/cloud/agent/0-agent-skill-binding.md` (status `proposed`). **No implementation code, no schema change.**
The Execution snapshot ADR `specs/decisions/cloud/skills/20260927-execution-skill-snapshot.md` was revised to consume
durable `AgentSkillBinding` only — its temporary explicit-skill-id admission fallback was removed and caller-supplied
`skill_id[]` is now forbidden.

- [x] **Agent ownership**: durable `agents` resource owned by one Collaboration Workspace (`collab_workspaces`),
      never Runtime `workspaces`/`projects`.
- [x] **AgentSkillBinding**: references `skill_id` (Skill identity), never `skill_revision_id`/digest/locator;
      `PRIMARY KEY(agent_id, skill_id)` = at most one binding.
- [x] **Mutation semantics**: add/enable/disable/remove + optimistic version (428/409) + Idempotency-Key.
- [x] **Soft-delete**: historical `ExecutionSkillBinding` remains valid; future admission excludes soft-deleted
      Skill; soft-deleted Agent denies new executions.
- [x] **Snapshot authority**: Execution request carries `agent_id` only, never Skill IDs; admission reads durable
      `AgentSkillBinding` only.
- [x] **Authorization**: reuses `collab_workspace_members` roles (owner/admin mutate; member read; non-member 404).
- [x] **ActorRef**: `agents.id` maps to `ActorRef{agent}`; taxonomy unchanged; `CollaborationDirectory` stays unwired.
- [x] **Schema impact**: design-only (`agents` + `agent_skill_bindings` forward migration); NO migration written.
- [x] **Implementation** (Agent CRUD/binding API, migration) — delivered in Step 4B below.

### Step 4B Agent & AgentSkillBinding — IMPLEMENTED

Status: **implemented**. The minimal durable `Agent` + `AgentSkillBinding` authority frozen by
`specs/decisions/cloud/agent/0-agent-skill-binding.md` is now code: forward migration
`0017_agents_and_skill_bindings.sql`, `internal/core/agents.go`, and the workspace-scoped public API + generated
contract below. It resolves **only** durable `AgentSkillBinding` as the Execution selection authority (ADR D8);
it implements **no** Execution/Attempt/ExecutionSkillBinding/capability/locator fields (ADR D13/D14).

**Repo-consistent API contract** (recorded per plan §13; endpoint paths follow the existing
`/tenants/:tid/spaces/:spaceId/...` workspace-scoped convention of `0015`/`spaces`):

| Method | Path (under `/api/v1/tenants/:tid/spaces/:spaceId`) | Body fields | Auth | Idempotency |
| --- | --- | --- | --- | --- |
| GET | `/agents` | — | member+ | — |
| POST | `/agents` | `name` | owner/admin | Idempotency-Key |
| GET | `/agents/:agentId` | — | member+ | — |
| PATCH | `/agents/:agentId` | `name`,`status`,`version` | owner/admin | version 428/409 |
| DELETE | `/agents/:agentId` | `version` | creator or owner/admin | Idempotency-Key + version |
| GET | `/agents/:agentId/skills` | — | member+ | — |
| POST | `/agents/:agentId/skills` | `skillId` | owner/admin | Idempotency-Key |
| PUT | `/agents/:agentId/skills/:skillId` | `enabled`,`version` | owner/admin | version 428/409 |
| DELETE | `/agents/:agentId/skills/:skillId` | — | owner/admin | Idempotency-Key (idempotent detach) |

Authorization reuses `workspaceRole` (`internal/core/space_permission.go`): non-member → `404 not_found` (no
existence leak, ADR D11); member-but-not-admin mutation → `403 workspace_admin_required`; delete uses
`workspaceCanDelete` (creator or owner/admin, ADR D11). Attach enforces same-workspace
(`agent.workspace_id == skill.workspace_id`) and not-soft-deleted (ADR D4); a binding to a Skill with no
`current_revision_id` is permitted (intent vs availability, ADR D4) and the failure is deferred to Execution
admission (`skill_revision_not_available`, not implemented here).

Binding mutation semantics (ADR D5): attach INSERT `enabled=true` (duplicate `(agent_id,skill_id)` → `409
binding_exists`; same idempotency key replays); enable/disable = `PUT {enabled,version}` (missing `428`, mismatch
`409`); detach = DELETE (absent → `404 binding_not_found`; same idempotency key replays). Soft-delete keeps binding
rows; the selection helper below excludes soft-deleted Skills and disabled bindings.

- [x] **Migration** `0017_agents_and_skill_bindings.sql`: `agents` (workspace-owned, `name` 1..128,
      `status active|disabled`, `version`, soft `deleted_at`, `UNIQUE(workspace_id,name) WHERE deleted_at IS NULL`,
      immutable-ownership trigger) + `agent_skill_bindings` (`PRIMARY KEY(agent_id,skill_id)`, `enabled`,
      `version`, no `deleted_at`). `0015_skills.sql`/`0016` unchanged.
- [x] **Core** `internal/core/agents.go`: create/read/list/patch/archive Agent; list/attach/enable-disable/detach
      binding; plus the `enabledAgentSkillBindings` read seam (enabled bindings joined to their live Skill,
      deterministically ordered by `canonical_name` then `skill_id`) that future Execution admission calls — it
      resolves **no** revision/digest (ADR D8/D10/D13; not a snapshot).
- [x] **No execution surface**: no `Execution`/`Attempt`/`ExecutionSkillBinding`, no capability/signed-URL/object
      locator, no caller-supplied `skill_ids` anywhere in the Agent tables or API (ADR D8/D13/D14).
- [ ] RetrievalCapability / Node delivery / Agent UI remain NOT implemented (later phases; the Execution snapshot
      was delivered by Phase 5).

Step 5B (Execution snapshot, Phase 5) is now landed: with its durable `AgentSkillBinding` selection authority in
place, the Execution/Attempt/ExecutionSkillBinding snapshot is implemented (see Phase 5 below).

## Phase 5 — Execution snapshot

Status: **implemented**. The logical `Execution` / physical `Attempt` / immutable `ExecutionSkillBinding` model frozen
by `specs/decisions/cloud/skills/20260927-execution-skill-snapshot.md` (status `implemented`) is now code: forward
migration `0018_execution_snapshot.sql`, `internal/core/executions.go` (the `AdmitExecution` / `CreateRetryAttempt` /
`RunAgainExecution` Store seam), and the `SkillRunSpec`/`SkillBundleRef` extension of the internal control contract.
It implements **no** RetrievalCapability minting, dispatch/claim/lease plumbing, or credential/object-locator field
(ADR D8/D7): the durable PostgreSQL snapshot is the sole authority later capability issuance is authorized against.

- [x] **Migration** `0018_execution_snapshot.sql`: `executions` (logical identity — no version/updated_at/deleted_at),
      `attempts` (physical, `ordinal` + state machine, `UNIQUE(execution_id, ordinal)`), and
      `execution_skill_bindings` (immutable fact, composite FK `(skill_revision_id, skill_id)`, trigger-blocked
      UPDATE, no bearer-credential field). `0015`/`0016`/`0017` unchanged.
- [x] **Core** `internal/core/executions.go`: `AdmitExecution` resolves durable enabled bindings → exact current
      revision (fail-closed `skill_revision_not_available`), then writes Execution + first eligible Attempt + all
      bindings atomically; `CreateRetryAttempt` reuses the frozen bindings without re-resolving; `RunAgainExecution`
      creates a new Execution and re-resolves. `skillBundles` returns the frozen descriptor ordered by
      `canonical_name` then `skill_id` (ADR D4/D12).
- [x] **Proto** `ExecutionInput.oneof spec` gains `SkillRunSpec skill_run = 2` carrying `SkillBundleRef[]`; generated
      `internal/controlpb` regenerated and reconciled (pinned plugins, `protoc (unknown)`).
- [x] **Tests** `integration/execution_snapshot_test.go` pins the stable anchors in
      `specs/test-cases/cloud/skills/execution-skill-snapshot.md` plus migration fresh/upgrade.
- [ ] RetrievalCapability minting / dispatch-claim-lease / Node delivery / Agent UI remain NOT implemented (later
      phases).

Frozen first-Attempt contract (ADR D3):

- admission creates `Execution` + **first `Attempt`** + all `ExecutionSkillBinding` rows in one database-only
  transaction — no Node/Controller external effect (HTTP/RPC/process/capability/Object Storage) inside it;
- first `Attempt` starts durable `eligible` with empty `node_id` / lease / epoch / dispatch metadata;
- Controller only claims/dispatches an **existing** Attempt; it never creates the first Attempt;
- retry creates a new Attempt (`ordinal` increments) reusing the same Execution frozen snapshot;
- crash recovery: an admitted Execution always has both the complete Skill snapshot and the first Attempt — there is
  no legal "admitted but no first Attempt" state.

## Phase 6 — Retrieval capability

### Step 5A RetrievalCapability contract — FROZEN (design only)

Status: **contract frozen; Cloud-side implementation landed in Step 5B; all changes remain uncommitted**. The
RetrievalCapability contract is closed by
`specs/decisions/controller/skill-delivery/0-skill-retrieval-capability.md` (status `implemented`) and its Node-side
coherence contract `specs/decisions/node/agent-runtime/0-skill-materialization-and-readiness.md` (status `proposed`),
with mirror test-cases under `specs/test-cases/controller/skill-delivery/` and `specs/test-cases/node/agent-runtime/`.
Step 5A moves these two ADRs (previously registered under `cloud/skills/`) to their canonical Controller/Node leaf
domains and closes the remaining open questions. **No schema change (ADR D13).**

- [x] **Authorization basis**: capability only for revisions frozen into the Execution `ExecutionSkillBinding`;
      never re-resolve `Skill.current_revision_id` at claim/dispatch time.
- [x] **Scope**: one immutable object, GET only; no bucket/prefix/workspace-wide or arbitrary-key access.
- [x] **Representation**: short-lived signed HTTPS GET URL (bearer credential, vendor-neutral, not S3-named).
- [x] **TTL**: default 300s, hard ceiling 900s; `expires_at` carried explicitly; covers dispatch + Node scheduling
      + retrieval + clock skew + 1–2 refresh-retry cycles.
- [x] **Refresh**: same Execution + Attempt + frozen revision → new capability; credential changes, not
      revision/locator/binding.
- [x] **Persistence/logging**: capability is never durable business state and never logged (redaction).
- [x] **Controller**: coordinates + relays; does not proxy bytes; data plane is Node→Object Storage direct.
- [x] **Provider boundary**: `ObjectStore` port unchanged; new `RetrievalCapabilityIssuer`-style boundary;
      `object_locator` stays a logical key, never a URL.
- [x] **Fencing/revocation**: fencing does not revoke a minted bearer URL; V1 revocation = credential expiry only
      (TTL is the exposure window); no immediate-revocation promise.
- [x] **Failure taxonomy**: storage_not_configured / revision_not_bound / attempt_not_eligible /
      object_not_available / signing_failed / invalid_locator / expired_or_retry_required / authorization_failed /
      temporary_control_plane_failure.
- [x] **Missing object**: no Stat-before-issuance; Node 404 → `object_not_available`, fail closed.
- [x] **Schema impact**: NONE.
- [x] **Cloud-side implementation** (capability mint/refresh) delivered in Step 5B below; Node downloader/cache/READY
      barrier remain later phases.

### Step 5B RetrievalCapability implementation — IMPLEMENTED (Cloud side)

Status: **implemented (Cloud-side mint/refresh); Node downloader/cache + Controller relay remain Phase 6/7**. The
Cloud side of `0-skill-retrieval-capability.md` is now code: `internal/skillstore/retrieval.go`
(`RetrievalCapabilityIssuer` port + redacted `RetrievalCapability` value), `internal/skillstore/s3store/issuer.go`
(S3 `PresignGetObject` exact-object GET mint), `internal/core/retrieval.go` (`MintSkillRetrievalCapabilities`:
transact-resolved `(execution, attempt, skill_revision)` authority → parse frozen `object_locator` → sign), the
`MintSkillRetrieval` control-gRPC RPC + D32 error-class mapping, config `storage.retrieval_capability_ttl` (default
300s / max 900s), and `cmd/server` issuer wiring. No persistence, no schema change, no Stat-before-issuance, no
credential logging (redacted `String()` + class-only fault messages).

- [x] **Issuer boundary** `RetrievalCapabilityIssuer` (`Issue(ctx, Locator, ttl) → {URL, Method, ExpiresAt}`) — separate
      from durable `ObjectStore`; `ObjectStore` port unchanged (no Presign added).
- [x] **S3 mint** `s3store.NewIssuer` + `Issue`: `PresignGetObject` over exact bucket + logical `locator`, GET-only, TTL
      clamped to configured bound; no existence probe.
- [x] **Authority** `resolveRetrievalTargets`: `(execution_id, attempt_id)` must name a live non-terminal Attempt, and
      each `skill_revision_id` must be a frozen `ExecutionSkillBinding` revision whose `object_locator` parses;
      otherwise fail closed (`authorization_failed`/`revision_not_bound`/`attempt_not_eligible`).
- [x] **Terminal fencing**: `succeeded/failed/canceled/superseded` attempts never mint (409 `attempt_not_eligible`).
- [x] **No persistence / redaction**: mint/refresh adds no durable row and no credential column exists; bearer URL never
      enters logs or durable state.
- [x] **Failure classes** `storage_not_configured`/`revision_not_bound`/`attempt_not_eligible`/`signing_failed`/
      `invalid_locator`/`authorization_failed` mapped in `internal/controlgrpc/fault.go`; provider detail never leaks.
- [x] **gRPC** `MintSkillRetrieval(execution_id, attempt_id, skill_revision_id[]) → capabilities[]`; request carries no
      locator/bucket/key/URL.
- [x] **Config** `storage.retrieval_capability_ttl` (0→300s default; `<=0` or `>900s` rejected).
- [x] **Tests** `internal/skillstore/retrieval_test.go` (redaction), `internal/skillstore/s3store/issuer_test.go`
      (exact-object GET / signing error / non-positive TTL / real SDK signed URL),
      `integration/retrieval_capability_test.go` (authorization, terminal fencing, storage_not_configured,
      invalid_locator, no-persistence, refresh-preserves-revision).
- [ ] Node downloader/cache, Controller relay (no proxy/select), READY barrier — later phases.

## Phase 7 — Node verified cache

- retrieval;
- staging;
- digest/size verification;
- atomic publish;
- concurrency;
- bounded GC.

## Phase 8 — Agent runtime projection

- AgentRuntimeAdapter;
- per-Attempt projection staging;
- atomic publish;
- ownership/preservation;
- READY barrier;
- spawn gate.

## Phase 9 — Frontend

- Skills board;
- directory/archive import UX;
- batch summary;
- Skill metadata/revision display;
- Agent assignment UI as scoped for this wave.

## Phase 10 — End-to-end audit

- full gates;
- core-test evidence;
- OpenAPI/client drift;
- both Git repositories status/diff;
- secret scan/log review;
- plan acceptance audit.

Implementation may split these phases into multiple commits, but must preserve coherent independently correct states.

---

# 35. STOP conditions

The implementation agent MUST stop and report before proceeding if any of these occur:

- approved ADR contradicts Workspace ownership;
- current execution model cannot represent immutable SkillRevision inputs without a semantic change beyond this plan;
- Agent persistence model differs materially from the assumed integration point;
- signed capability cannot be transported without violating an approved protocol that has not yet been updated;
- object-store client lacks a safe stable-identity reconciliation mechanism;
- Runtime/Agent discovery requires modifying canonical Skill package bytes in a non-deterministic way;
- existing Node lifecycle cannot enforce the READY-before-spawn gate without a larger architecture change;
- schema naming/ownership conflicts with an already implemented first-class Skills model;
- any proposed implementation requires external filesystem/HTTP/Object Storage calls inside a DB transaction;
- any proposed shortcut would weaken auth, idempotency, versioning, fencing, migration checks, or test gates.

---

# 36. Acceptance criteria

## Domain

- [ ] Every Skill belongs to exactly one Collaboration Workspace.
- [ ] Active canonical Skill names are unique within a Workspace.
- [ ] Skill metadata is mutable and SkillRevision content is immutable.
- [ ] Same Skill + same canonical digest reuses one SkillRevision.
- [ ] Different Skills remain distinct even with identical content.
- [ ] Skill is soft-deleted and existing execution snapshots remain stable.

## Ingestion

- [ ] Directory and archive inputs use one canonical virtual-tree pipeline.
- [ ] Recursive Skill discovery works.
- [ ] Nested Skill roots fail.
- [ ] Unsafe paths/special nodes/symlinks fail.
- [ ] Binary supporting files are preserved.
- [ ] Canonical digest ignores transport/archive metadata.
- [ ] Batch import is partial-success.
- [ ] Each candidate has independent durable ingestion evidence.
- [ ] External object work occurs outside DB transactions.
- [ ] Ambiguous external outcomes reconcile by stable identity.

## Execution

- [ ] AgentSkillBinding is mutable future-execution configuration.
- [ ] Execution creation resolves exact immutable SkillRevision rows.
- [ ] ExecutionSkillBinding is immutable.
- [ ] dispatch/claim never recomputes Skills from mutable Agent configuration.
- [ ] retry creates a new Attempt under the same Execution.
- [ ] retry preserves exact SkillRevision bindings.
- [ ] Run Again/new Execution resolves current configuration.

## Retrieval/security

- [ ] Node receives an abstract short-lived retrieval capability, not long-lived storage credentials.
- [ ] Signed capability handling is explicitly approved in specs/ADR.
- [ ] capability is not persisted or logged.
- [ ] refresh can only mint access to the same bound revision.
- [ ] Node verifies size and digest independently.
- [ ] Controller/Cloud do not proxy normal Skill package bytes.

## Node

- [ ] Verified cache is digest-addressed, immutable, atomic, concurrency-safe, disposable, and bounded.
- [ ] Agent never mutates shared cache.
- [ ] projections are Attempt-scoped.
- [ ] projection uses staging then atomic publish.
- [ ] partial projection is never Agent-visible.
- [ ] required Skill failure blocks spawn.
- [ ] READY barrier precedes Agent spawn.
- [ ] crash recovery rebuilds/revalidates; no partial filesystem resume.
- [ ] Agent-specific discovery logic lives in AgentRuntimeAdapter.
- [ ] materialization never executes Skill package content.

## Compatibility / quality

- [ ] Relevant specs ADRs and core tests are synchronized.
- [ ] applied migrations were not modified.
- [ ] fresh DB migration test passes.
- [ ] upgrade migration test passes.
- [ ] OpenAPI/frontend client generated artifacts are synchronized where applicable.
- [ ] narrow tests pass.
- [ ] `task format` passes.
- [ ] `task check` passes for behavior/repository-wide changes.
- [ ] `task test:race` passes for concurrency/recovery/cache/persistence changes.
- [ ] `task build` passes for applicable command/startup changes.
- [ ] frontend gates pass for frontend changes.
- [ ] `git diff --check` passes.
- [ ] complete diffs reviewed.
- [ ] root repo and `specs/` repo statuses reviewed.
- [ ] no secrets/capabilities leaked into logs, fixtures, snapshots, or committed files.
- [ ] no unrelated user changes overwritten.

---

# 37. Final implementation report format

The implementing Agent must finish with:

```text
## Plan Audit

### Implemented
- <plan section> — PASS — <files/tests/evidence>

### Not Applicable
- <plan section> — N/A — <reason>

### Deviations
- NONE
```

If there is any deviation:

```text
### Deviations
- <plan section>
  Planned:
  Implemented:
  Reason:
  Approval/reference:
```

Unapproved semantic deviations are a failure, not an implementation choice.

Also report migrations added, ADR/spec changes, APIs/contracts changed, tests added/updated, commands/gates run and their results, root `git status --short`, `git -C specs status --short`, and known residual risks/deferred non-goals.
