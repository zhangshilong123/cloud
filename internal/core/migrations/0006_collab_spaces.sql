CREATE TABLE collab_workspaces (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
 slug text NOT NULL CHECK(slug ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
 description text NOT NULL DEFAULT '',
 created_by uuid NOT NULL REFERENCES users(id),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 archived_at timestamptz,
 UNIQUE(id,tenant_id),
 UNIQUE(tenant_id,slug)
);
CREATE INDEX collab_workspace_list ON collab_workspaces(tenant_id,id);
CREATE TABLE collab_workspace_members (
 workspace_id uuid NOT NULL REFERENCES collab_workspaces(id),
 user_id uuid NOT NULL REFERENCES users(id),
 role text NOT NULL CHECK(role IN ('owner','admin','member')),
 status text NOT NULL CHECK(status IN ('active','disabled')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_by uuid,
 joined_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,user_id)
);
CREATE INDEX collab_workspace_member_user ON collab_workspace_members(user_id);
