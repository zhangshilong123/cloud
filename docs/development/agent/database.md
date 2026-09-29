# Database — for agents

PostgreSQL 17. Migrations are embedded, checksummed, applied in order by `Store.Migrate()` /
`cloudctl migrate`, recorded in `schema_migrations`. **Immutable once applied** — never edit an
applied file; add a new `NNNN_*.sql`.

## Migrations

| File | What it adds |
| --- | --- |
| `0001_core.sql` | tenants, users, user_identities, tenant_memberships, credential_refs, projects, project_storage, workspaces, workspace_worktrees, tasks, operations, external_effects, execution_tickets, sandbox_instances, node_instances, controller_leases, idempotency_records |
| `0002_aggregate_guards.sql` | deferred-constraint triggers (one main workspace per project, last-admin guard, …) |
| `0003_resource_versions.sql` | `version` columns + optimistic-concurrency backfill |
| `0004_effect_intent_and_ticket_scope.sql` | effect intent + ticket scoping hardening |
| `0005_gateway_auth.sql` | Gateway authentication tables: `gateway_login_attempts`, `gateway_sessions` (runtime-only access by `cmd/gateway`) |
| `0006_collab_spaces.sql` | **Collaboration Spaces** (upstream baseline, byte-identical to `upstream/main`): `collab_workspaces` (id, tenant_id, name, slug, description, created_by, version, archived_at; `UNIQUE(id,tenant_id)`, `UNIQUE(tenant_id,slug)`) and `collab_workspace_members` (workspace_id, user_id, role owner/admin/member, status active/disabled, version, created_by, joined_at; `PRIMARY KEY(workspace_id,user_id)`) — the Workspace resource-sharing boundary |
| `0007_project_space_scope.sql` | **Project → Space scoping** (upstream baseline, byte-identical to `upstream/main`): seeds a default Collaboration Space for every live tenant (+ its active members as owner/member), adds `projects.space_id uuid NOT NULL`, binds every existing project to the default Space, composite FK `(space_id,tenant_id) → collab_workspaces(id,tenant_id)` + index `project_space_list(space_id,id)` |
| `0008_issues.sql` | `issues` (board) — formerly `0006_issues.sql` (renumbered in the migration reconciliation so upstream 0001–0007 stay byte-identical; see `docs/migrations/workspace-integration-stage-a.md`) |
| `0009_issue_extensions.sql` | issue_statuses, issue_comments, labels, issue_labels, issue_subscribers, issue_views + `issues` ALTERs (`number`, `properties`, status format check) — formerly `0007_issue_extensions.sql` |
| `0010_issue_collaboration.sql` | `issues` ALTERs (`assignee_type`/`assignee_id`/`project_ref` + backfill), `issue_comments` ALTERs (`parent_id`/`author_type`/`author_id`/`seq` + backfill + `UNIQUE(issue_id,seq)`), new tables `issue_runs`, `issue_activities`, `issue_context_refs` — formerly `0008_issue_collaboration.sql` |
| `0011_issue_interactions.sql` | new table `issue_interactions` (the `@` interaction spine) — one row per selected collaboration target: `id, tenant_id, issue_id, comment_id, target_type, target_id, mode, task, run_id, created_at` — formerly `0009_issue_interactions.sql` |
| `0012_issue_interaction_input.sql` | one generic additive column: `ALTER TABLE issue_interactions ADD COLUMN input jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(input)='object')` — the confirmed form values ([§38.30](../../migrations/multica-issue-board/12-collaboration-architecture.md#3830-does-3b-2-need-a-migration--yes-one-additive-column)). Deliberately excludes `version`, a `status` enum, `confirmed_at` and a separate inputs table. `0011` is not modified. — formerly `0010_issue_interaction_input.sql` |
| `0013_project_space_optional.sql` | **Project Space optional (append-only compatibility)**: `projects.space_id` back to **NULLABLE** — upstream `0007` imposed `NOT NULL` + full project binding; PS3/D2=C keeps Space an optional grouping. `0013` only relaxes the constraint; it deliberately **does NOT unbind** projects `0007` already assigned (no data change, no scope shrink). Space-scoped projects stay workspace-shared (Step 3 access model); new unscoped projects stay owner-only |
| `0014_clone_coordination.sql` | clone coordination via the internal control contract (appended after upstream `0008–0013`): `clone_requests` (work Cloud accepted in its own business tx, idempotent on `(tenant,user,request_id)`, state `queued→dispatched→succeeded/failed`), `clone_executions` (exactly one per request, Controller-chosen opaque identities, input + terminal result + lease epoch), `clone_event_receipts` (exact `(execution,sequence,event)` receipts), `control_submissions` (identity + request digest + recorded response; same identity+content replays instead of reapplying) |
| `0015_skills.sql` | **Cloud Skills persistence foundation** (appended after `0013`; follows `specs/decisions/cloud/skills/0-cloud-skills.md`): `skills` (mutable, owned by exactly one Collaboration Workspace), `skill_revisions` (immutable content snapshots), `skill_ingestions` (journal-first upload/import saga). **Schema only** — see the Cloud Skills inventory below; no HTTP API / Object Storage / ingestion pipeline yet |
| `0016_skill_ingestion_idempotency.sql` | **Cloud Skills ingestion idempotency & recovery** (appended after `0015`; follows `specs/decisions/cloud/skills/20260927-canonical-ingestion-saga.md` Schema section): four additive `skill_ingestions` changes — `canonical_name` (nullable, 1–200), `request_fingerprint` (1–128), `activation_outcome` (`activated`/`activation_conflict`), `UNIQUE(workspace_id, idempotency_key, canonical_name)`. `0015_skills.sql` is not modified |

## Table inventory

### Identity & tenancy
| Table | Purpose | Key columns |
| --- | --- | --- |
| `tenants` | org boundary | id, name, status, deleted_at |
| `users` | person | id, display_name, status, deleted_at |
| `user_identities` | external identity → user | user_id, source, subject |
| `tenant_memberships` | tenant↔user + role | tenant_id, user_id, role(admin/member), status |
| `credential_refs` | named secret references | tenant_id, owner_user_id, purpose, ref |

### Projects / workspaces / operations
| Table | Purpose | Key columns |
| --- | --- | --- |
| `projects` | dev-environment repo | tenant_id, owner_user_id (creator), name, repository_url, default_branch, lifecycle, **space_id?** (nullable FK → collab_workspaces; set = workspace-shared, NULL = legacy owner-only) |
| `project_storage` | per-project volume state | project_id, observed_state |
| `workspaces` | main/isolated worktree env | project_id, tenant_id, owner_user_id, kind, desired/observed_state, runtime_generation |
| `workspace_worktrees` | git worktree metadata | workspace_id, branch_name, base_commit_id |
| `tasks` | isolated workspace title | workspace_id, title |
| `operations` | durable async work | tenant_id, project_id, workspace_id, kind, state, step, version |
| `external_effects` | external-action plan/evidence | operation_id, kind, state, external_id, request, result |
| `execution_tickets` | node execution lease | workspace_id, state |
| `sandbox_instances` | running sandbox | workspace_id, … |
| `node_instances` | registered nodes | connection_state, initialized, heartbeat |
| `controller_leases` | controller leadership | epoch fencing |
| `idempotency_records` | POST/DELETE replay | tenant_id, user_id, key, request_hash, response, status |

### Collaboration Spaces (0006) — the resource-sharing boundary
| Table | Purpose | Key columns |
| --- | --- | --- |
| `collab_workspaces` | the Workspace that groups and shares resources | id, tenant_id, name, slug, description, created_by, version, archived_at · `UNIQUE(tenant_id,slug)` |
| `collab_workspace_members` | Workspace membership (the sharing boundary) | (workspace_id,user_id) PK, role owner/admin/member, status active/disabled, version, joined_at |

**Workspace = resource-sharing boundary** (Step 2B model; implemented on Projects in Step 3):
`projects.space_id → collab_workspaces` opts a project into a Workspace; any **active member** of that
Workspace may read the project and its runtime workspaces; the **creator or a workspace owner/admin**
may delete it. Per-resource membership (`project_members` / …) is **NOT used**. Not every
`collab_workspaces` column implies an exposed feature — the DB is ahead of the HTTP surface by design.

### Issue board (0008–0012)
| Table | Purpose | Key columns |
| --- | --- | --- |
| `issues` | board card | tenant_id, creator_user_id, assignee_type/assignee_id (polymorphic), assignee_user_id? (mirror), project_ref?, parent_issue_id?, title, description, status, priority, position, number, properties(jsonb), version, deleted_at |
| `issue_statuses` | status catalog | tenant_id, key, name, category, color, icon, is_system, position, deleted_at · UNIQUE(tenant_id,key) |
| `issue_comments` | comment thread | tenant_id, issue_id, author_user_id? (mirror), author_type/author_id (ActorRef), parent_id? (threading), body, seq, version, deleted_at · UNIQUE(issue_id,seq) |
| `issue_runs` | issue-owned run lifecycle | tenant_id, issue_id, executor_type/executor_id, status, external_execution_id?/execution_context_ref?/workflow_invocation_ref? (opaque refs), input/result/error, trigger_evidence_*, version, deleted_at |
| `issue_activities` | append-only timeline projection | tenant_id, issue_id, actor_type/actor_id, action, details(jsonb), seq, created_at |
| `issue_context_refs` | issue→external reference | tenant_id, issue_id, ref_type, ref_id, created_at · hard delete |
| `issue_interactions` | `@` interaction spine | tenant_id, issue_id, comment_id, target_type/type, target_id, mode(mention/task/form), task, run_id?, input(jsonb) · one row per selected target · `run_id IS NULL` = unconfirmed, `input` = confirmed form values |
| `labels` | tenant label | tenant_id, name, color, deleted_at · partial UNIQUE(tenant_id,name) WHERE deleted_at IS NULL |
| `issue_labels` | issue↔label join | (issue_id,label_id) PK · hard delete |
| `issue_subscribers` | issue watchers | (issue_id,user_id) PK, tenant_id |
| `issue_views` | saved filter | tenant_id, owner_user_id, name, filter(jsonb), version, deleted_at |

### Cloud Skills (0015) — persistence foundation only

| Table | Purpose | Key columns |
| --- | --- | --- |
| `skills` | a mutable Skill owned by **exactly one Collaboration Workspace** (`collab_workspaces`, never the runtime `workspaces`) | workspace_id (immutable via `skill_immutable` trigger), canonical_name, display_name, summary, current_revision_id (composite FK → `skill_revisions(id, skill_id)`), version, created_by, deleted_at · partial `UNIQUE(workspace_id, canonical_name) WHERE deleted_at IS NULL` |
| `skill_revisions` | immutable canonical content snapshots of one Skill | skill_id, content_digest, digest_algorithm (default `sha256`), size_bytes, file_count, package_format, package_format_version, object_locator, package_name, package_description, created_by · `UNIQUE(skill_id, digest_algorithm, content_digest)`; deliberately **no** `version`/`updated_at`/`deleted_at` |
| `skill_ingestions` | journal-first upload/import saga evidence (kept apart from `skill_revisions`) | workspace_id, target_skill_id (nullable), idempotency_key, source_type (`directory`/`archive`), canonical_name (0016), request_fingerprint (0016), expected_digest, digest_algorithm, object_locator, state (`planned`→`storing`→`verified`→`committed`/`failed`), activation_outcome (0016), error_code, error_detail, created_by · `UNIQUE(workspace_id, idempotency_key, canonical_name)` |

**Persistence-only.** `0015` creates the domain tables and constraints and pins their SQL-enforceable
invariants. Since then the ingestion saga (`Store.IngestSkill`/`IngestSkills`), `SKILL.md` discovery
(`internal/skillsource`), and the public upload HTTP API (`POST …/skills/imports`, Step 3B) have been
layered on top; there is still **no** `AgentSkillBinding` / `ExecutionSkillBinding`, no execution snapshot
or `RetrievalCapability` (a bearer credential that must never be persisted), and no frontend Skills board
yet. Do not read these tables as an exposed feature — same additive-first rule as the collaboration tables
below. ADRs:
`specs/decisions/cloud/skills/0-cloud-skills.md` (status `proposed`),
`specs/decisions/cloud/skills/20260924-canonical-skill-package-v1.md` (the canonical package
encoder/digest now lives in `internal/skillpkg`; status `proposed`), and
`specs/decisions/cloud/skills/20260927-object-storage-abstraction.md` (status `proposed`).

**Object Storage abstraction: implemented semantics; the saga now drives the port — still no real provider.**
Step 2B implemented the frozen *semantics* as code (`internal/skillstore` port / semantic types / reconciliation
core, plus the `internal/skillstore/fakestore` test double). Step 2C now drives that port from the ingestion saga
(`Store.SkillsObjectStore`, `PutImmutable`/`Reconcile`), so integration tests write and verify real package bytes
through the in-memory double. There is still **no** Object Storage client, configuration key, SDK dependency, or
upload HTTP API — production wiring is a later step. `object_locator` is a provider-independent **logical object
key** derived from the package bytes
(`skills/<package_format>/v<version>/<digest_algorithm>/<package_digest>`, ≤ 277 bytes under the column bounds, so
the existing `<= 1024` CHECK holds); `content_digest` is the Step 2A **tree** digest and is never
`SHA256(package bytes)`; `size_bytes` / `file_count` describe the canonical tree rather than the container;
`skill_revisions.object_locator` must be non-empty for saga-created rows. There is still no `RetrievalCapability`
or Node delivery.

**Ingestion saga: implemented** (`internal/core` `Store.IngestSkill` / `IngestSkills`, following
`specs/decisions/cloud/skills/20260927-canonical-ingestion-saga.md`, status `proposed`). The `0016` migration
(applied) adds `canonical_name`, `request_fingerprint` (the semantic request fingerprint over
`(target_skill_id, display_name, summary, content_digest, package_digest)`), `activation_outcome`
(`activated`/`activation_conflict`), and `UNIQUE(workspace_id, idempotency_key, canonical_name)` — as a new file,
never an edit to `0015_skills.sql`. The saga is journal-first and idempotent: TX #1 authorizes (Workspace
owner/admin) and inserts a `planned` row or finds the existing one (replay / resume / conflict); the Object
Storage phase probes by stable identity, PUTs only when definitively absent, and reconciles to
`CONFIRMED_PRESENT_MATCHING` **outside any DB transaction**; TX #2 creates/reuses the Skill and SkillRevision,
runs the activation CAS, and finalizes `committed` + `activation_outcome`. Recovery is **client-driven
continuation** (no server-side background worker): the canonical package bytes are deterministically rebuilt from
a same-key resubmission, so PostgreSQL still never stores package bytes, and a `planned`/`storing`/`verified`
strand converges when the same `Idempotency-Key` re-submits.

**SKILL.md metadata contract: implemented in `internal/skillmeta`** (`specs/decisions/cloud/skills/20260927-skill-md-metadata-contract.md`,
status `implemented`): `canonical_name = NFC(case-fold(NFC(name)))` is the single transformation from `SKILL.md` bytes.
`name` is required and must be a YAML string that is valid UTF-8 (not starting with `.`, no control characters, ≤ 200 bytes);
`description` is optional (string, ≤ 4096 bytes); duplicate keys and invalid YAML fail closed; unknown fields are
opaque and preserved. `internal/skillmeta` is the business metadata layer and stays separate from
`internal/skillpkg` (which stays frozen and does not parse `name`). Compatible with Desktop
`0-static-skill-package.md` D3 and all audited multica content.

**Source intake & candidate discovery: implemented in `internal/skillsource` (no schema change).**
`specs/decisions/cloud/skills/20260927-source-intake-candidate-discovery.md` (status `proposed`, approved for
implementation) closes the four source-intake semantics that blocked the source-adapter slice (Step 2C.1) —
archive support set (ZIP + uncompressed TAR only, caller-supplied `SourceKind`), unattached-file handling
(ignored after safety validation), nested-root rejection, and duplicate `canonical_name` rejection — plus the
structural/preparation/business failure boundary. These are realized in `internal/skillsource`
(`PrepareDirectory` / `PrepareZip` / `PrepareTar` → `PreparedSourceResult{Candidates[],
PreparationFailures[]}`) feeding the existing `IngestSkills` saga through `core.Store.IngestSource`. It has
**no** schema impact: `0015_skills.sql` and `0016_skill_ingestion_idempotency.sql` are unchanged and no `0017`
was added. The public upload HTTP API is now implemented (Step 3B); a production Object Storage provider
remains unimplemented.

### Schema-ready vs. exposed (collaboration tables)

The DB is ahead of the HTTP surface on purpose — additive-first. Do not read a column's existence as
an implemented feature:

| Table / column | Persistence | API |
| --- | --- | --- |
| `issue_activities` | ✅ written internally (`appendActivity`) | ✅ `GET /issues/{iid}/timeline` (merged with comments) |
| `issue_comments.author_type` | ✅ CHECK allows `user/agent/team/system` | ✅ user + agent/team (internal run-reply path); `system` still unwritten |
| `issue_comments.seq` | ✅ shared per-issue namespace with `issue_activities.seq` | ✅ comment list and `/timeline` order by it |
| `issue_interactions` | ✅ migrations `0011` + `0012` | ✅ `GET /issues/{iid}/interactions`; written via `targets[]` on comment create, `input`/`run_id` claimed by confirm |
| `issue_runs` | ✅ full 7-state lifecycle columns | ⚠️ create/list/get; transitions driven by the (mock) dispatcher/observer, not a public state API |
| `issues.assignee_type` / `project_ref` | ✅ | ⚠️ agent/team/project refs are opaque, unresolved |

Collaboration semantics (interaction modes, `@`, cardinality, timeline) are frozen in
[12-collaboration-architecture.md §37](../../migrations/multica-issue-board/12-collaboration-architecture.md#37-wave-3b-0--collaboration-interaction-model-frozen).

## Conventions

- Every business table: `id uuid PK`, `version bigint DEFAULT 1 CHECK(version>0)`,
  `created_at`/`updated_at timestamptz DEFAULT now()`, soft-delete `deleted_at timestamptz`.
- **Scope = `tenant_id`**; composite FKs `(tenant_id, user_id) → tenant_memberships` bind a
  user-reference to a member-of-record.
- **Cascade behaviour is deliberate**: `issues.parent_issue_id … ON DELETE SET NULL` (orphan
  children), join tables hard-delete, business rows soft-delete.
- Query pattern: `t.list`/`t.one` → `SELECT row_to_json(resource) FROM (…) resource`, top-level
  keys camelCased, nested jsonb keys keep DB names.

## Adding a table / altering one

1. New file `internal/core/migrations/NNNN_descriptive.sql` (next number; embedded by Go embed).
2. Follow the column/constraint conventions above; add indexes for the read patterns you'll use.
3. To alter an existing table, `ALTER` in the **new** file — never edit the original.
4. `Store.Migrate()` applies pending files automatically; the upgrade test
   (`integration/cloud_test.go`) keeps a hardcoded **pre-upgrade** baseline list — add your file
   there only if the test intends to exercise upgrading *through* it, otherwise leave it.
5. If you add a tenant-scoped lookup that must exist per tenant (like the status catalog), seed it
   lazily in code (`ON CONFLICT DO NOTHING`), not in the migration.
