-- Issue board extensions (second wave): status catalog, comments, labels, subscribers,
-- saved views, plus per-tenant human-readable numbers and free-form properties on issues.

-- Status catalog replaces the fixed 7-value CHECK on issues.status with tenant-defined columns.
CREATE TABLE issue_statuses (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 key text NOT NULL CHECK(key ~ '^[a-z0-9][a-z0-9_]{0,31}$'),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 200),
 description text NOT NULL DEFAULT '',
 category text NOT NULL DEFAULT 'started' CHECK(category IN ('unstarted','started','done','closed')),
 color text NOT NULL DEFAULT '',
 icon text NOT NULL DEFAULT '',
 is_system boolean NOT NULL DEFAULT false,
 position double precision NOT NULL DEFAULT 0,
 version bigint NOT NULL DEFAULT 1 CHECK(version > 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 deleted_at timestamptz,
 UNIQUE(tenant_id, key)
);
CREATE INDEX issue_status_list ON issue_statuses(tenant_id, position, id);

-- Comments on an issue. Author must be an active tenant member (structural FK).
CREATE TABLE issue_comments (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 issue_id uuid NOT NULL REFERENCES issues(id),
 author_user_id uuid NOT NULL,
 body text NOT NULL CHECK(length(body) BETWEEN 1 AND 20000),
 version bigint NOT NULL DEFAULT 1 CHECK(version > 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 deleted_at timestamptz,
 FOREIGN KEY(tenant_id, author_user_id) REFERENCES tenant_memberships(tenant_id, user_id)
);
CREATE INDEX issue_comment_list ON issue_comments(issue_id, created_at, id);

-- Labels are tenant-scoped; issue_labels is the many-to-many link.
CREATE TABLE labels (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 200),
 color text NOT NULL DEFAULT '',
 version bigint NOT NULL DEFAULT 1 CHECK(version > 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 deleted_at timestamptz
);
CREATE UNIQUE INDEX label_name_uniq ON labels(tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX label_list ON labels(tenant_id, id);

CREATE TABLE issue_labels (
 issue_id uuid NOT NULL REFERENCES issues(id),
 label_id uuid NOT NULL REFERENCES labels(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(issue_id, label_id)
);

-- Subscribers: which users watch an issue. The watcher must be an active member of the tenant.
CREATE TABLE issue_subscribers (
 issue_id uuid NOT NULL REFERENCES issues(id),
 user_id uuid NOT NULL,
 tenant_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(issue_id, user_id),
 FOREIGN KEY(tenant_id, user_id) REFERENCES tenant_memberships(tenant_id, user_id)
);

-- Saved views: per-owner board filter specs.
CREATE TABLE issue_views (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 owner_user_id uuid NOT NULL,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 200),
 filter jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(filter) = 'object'),
 version bigint NOT NULL DEFAULT 1 CHECK(version > 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 deleted_at timestamptz,
 FOREIGN KEY(tenant_id, owner_user_id) REFERENCES tenant_memberships(tenant_id, user_id)
);
CREATE INDEX issue_view_list ON issue_views(tenant_id, owner_user_id, id);

-- issues: replace the fixed status enum with a format check (validated against issue_statuses),
-- and add per-tenant numbers plus free-form properties.
ALTER TABLE issues DROP CONSTRAINT issues_status_check;
ALTER TABLE issues ADD CONSTRAINT issues_status_format_check CHECK(status ~ '^[a-z0-9][a-z0-9_]{0,31}$');
ALTER TABLE issues ADD COLUMN number bigint;
UPDATE issues SET number = sub.rn FROM (SELECT id, row_number() OVER (PARTITION BY tenant_id ORDER BY created_at, id) AS rn FROM issues) sub WHERE issues.id = sub.id;
ALTER TABLE issues ALTER COLUMN number SET NOT NULL;
ALTER TABLE issues ADD CONSTRAINT issues_tenant_number_uniq UNIQUE(tenant_id, number);
ALTER TABLE issues ADD COLUMN properties jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(properties) = 'object');
