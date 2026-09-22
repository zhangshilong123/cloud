CREATE TABLE issues (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 creator_user_id uuid NOT NULL,
 assignee_user_id uuid REFERENCES users(id),
 parent_issue_id uuid REFERENCES issues(id) ON DELETE SET NULL,
 title text NOT NULL CHECK(length(title) BETWEEN 1 AND 200),
 description text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'todo' CHECK(status IN ('backlog','todo','in_progress','in_review','blocked','done','cancelled')),
 priority text NOT NULL DEFAULT 'none' CHECK(priority IN ('urgent','high','medium','low','none')),
 position double precision NOT NULL DEFAULT 0,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 deleted_at timestamptz,
 FOREIGN KEY(tenant_id, creator_user_id) REFERENCES tenant_memberships(tenant_id, user_id)
);
CREATE INDEX issue_board ON issues(tenant_id, status, position, id);
CREATE INDEX issue_list ON issues(tenant_id, id);