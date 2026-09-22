# Database Migrations Module

[中文](README.md) | [English](README.en.md)

This module contains Ora Cloud's linear, forward-only PostgreSQL schema migration catalog. Migrations are embedded directly into the Go application binary using `embed.FS` and applied deterministically by `cloudctl migrate`.

## Migration catalog

Migrations are executed in ascending numerical sequence:

- **`0001_core.sql`**: Foundational domain schema:
  - Identity & Access: `users`, `user_identities`, `tenants`, `tenant_memberships`, `credential_refs`.
  - Projects & Workspaces: `projects`, `project_storage`, `workspaces`, `workspace_worktrees`, `tasks`.
  - Execution runtime: `sandbox_instances`, `workspace_nodes`, `sessions`.
  - Control plane: `effects`, `operations`, `tickets`, `controller_leases`, `idempotency_keys`.
  - Invariants: Partial unique index `one_main` ensures at most one active `main` workspace per project. Foreign keys strictly enforce tenant and owner containment across all hierarchy tiers.
- **`0002_aggregate_guards.sql`**: Concurrency and mutual exclusion guards:
  - Prevents concurrent lifecycle mutations on the same project aggregate.
  - Ensures soft-deleted ancestors prevent active child state transitions.
- **`0003_resource_versions.sql`**: Optimistic concurrency controls:
  - Enforces `version` incrementing rules across mutable entities (`projects`, `workspaces`, `tasks`, `nodes`, `operations`).
  - Guards against lost updates in concurrent API operations.
- **`0004_effect_intent_and_ticket_scope.sql`**: Execution intent and ticket constraints:
  - Enforces strict scoping of execution tickets to active workspace nodes and valid admission epochs.
  - Binds durable effect declarations to specific operation phases.
- **`0005_gateway_auth.sql`**: Gateway authentication tables (accessed at runtime only by `cmd/gateway`):
  - `gateway_login_attempts`: one-shot login attempts; stores only SHA-256 digests of the attempt secret and `state`, rejects absolute, `//` and `/\` `return_to` values at the database layer, bounds the lifetime to one hour, and uses `consumed_at` to guarantee at most one session per attempt.
  - `gateway_sessions`: browser sessions; stores only the token digest, requires a non-null `expires_at` no later than 90 days after creation, keeps revocation time and the bounded `revoked_reason` together, and indexes identity revocation and bounded cleanup.
- **`0006_collab_spaces.sql`**: Collaboration space schema (product term Workspace):
  - `collab_workspaces`: tenant-scoped collaboration and visibility boundary (name, immutable slug, archive time, optimistic version).
  - `collab_workspace_members`: members with roles (owner/admin/member), status (active/disabled), and optimistic version.
  - Strictly separated from the runtime `workspaces` table (execution environments).
- **`0007_project_space_scope.sql`**: Project space scoping and data backfill:
  - Creates a default space (slug=`default`) for every existing tenant, including deleted tenants that still own projects.
  - Adds existing active tenant members to the default space (admin maps to owner, member maps to member).
  - Backfills `projects.space_id`, then enforces NOT NULL and a composite foreign key `(space_id, tenant_id)` that rejects cross-tenant ownership at the SQL level.
  - Runs `SET CONSTRAINTS ALL IMMEDIATE` before the ALTER to flush deferred constraint triggers queued by the backfill UPDATE.
- **`0008_issues.sql`**: the `issues` (board) base table.
- **`0009_issue_extensions.sql`**: `issue_statuses`, `issue_comments`, `labels`, `issue_labels`, `issue_subscribers`, `issue_views` + `issues` ALTERs (`number`, `properties`, status format check).
- **`0010_issue_collaboration.sql`**: `issues` ALTERs (`assignee_type`/`assignee_id`/`project_ref` + backfill), `issue_comments` ALTERs (`parent_id`/`author_type`/`author_id`/`seq` + backfill + `UNIQUE(issue_id,seq)`), new tables `issue_runs`, `issue_activities`, `issue_context_refs`.
- **`0011_issue_interactions.sql`**: new table `issue_interactions` (the `@` interaction spine) — one row per selected collaboration target: `id, tenant_id, issue_id, comment_id, target_type, target_id, mode, task, run_id, created_at`.
- **`0012_issue_interaction_input.sql`**: one generic additive column: `ALTER TABLE issue_interactions ADD COLUMN input jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(input)='object')` — the confirmed form values. Deliberately excludes `version`, a `status` enum, `confirmed_at` and a separate inputs table; `0011` is not modified.

> Renumbering note (upstream-merge alignment): this branch's collab-space migrations
> `0011_collab_spaces.sql` / `0012_project_space_scope.sql` overlapped upstream's
> `0006_collab_spaces.sql` / `0007_project_space_scope.sql`; **upstream numbering and semantics win**,
> so the duplicates were removed and only upstream's 0006/0007 remain. The Issue migrations were
> renumbered to keep the line strictly forward-ordered (schema semantics unchanged):
> `0006_issues.sql` → `0008_issues.sql`, `0007_issue_extensions.sql` → `0009_issue_extensions.sql`,
> `0008_issue_collaboration.sql` → `0010_issue_collaboration.sql`,
> `0009_issue_interactions.sql` → `0011_issue_interactions.sql`,
> `0010_issue_interaction_input.sql` → `0012_issue_interaction_input.sql`.

## Checksum integrity and immutability

- **`schema_migrations` table**: Tracks applied versions, their SHA256 checksums, and application timestamps (`version`, `checksum`, `applied_at`).
- **Server startup check**: At startup, `cmd/server` runs `store.CheckSchema`, verifying that:
  1. All embedded `.sql` migration files exist in `schema_migrations`.
  2. The SHA256 checksum of each embedded file matches the recorded checksum in the database.
  3. No unknown or extraneous migration versions exist in the database.
  If any mismatch or unapplied migration is found, the server terminates immediately.
- **No AutoMigrate**: The production server daemon **never** executes DDL or modifies table structures at startup. Migrations must be applied using `cloudctl migrate` under dedicated database administrator credentials.

See [core overview](../README.en.md), [cloudctl CLI](../../../cmd/cloudctl/README.en.md), and [Core contract](../../../docs/core-contract.md).
