-- Backfill a default collab workspace for every tenant that is live or still owns
-- projects, then bind every existing project to it. Deterministic: no online IDaaS calls.
INSERT INTO collab_workspaces(id, tenant_id, name, slug, description, created_by)
SELECT gen_random_uuid(), t.id, 'Default', 'default', '', COALESCE((
 SELECT m.user_id FROM tenant_memberships m WHERE m.tenant_id=t.id ORDER BY m.created_at, m.user_id LIMIT 1
), (
 SELECT u.id FROM users u ORDER BY u.id LIMIT 1
))
FROM tenants t
WHERE t.deleted_at IS NULL OR EXISTS (SELECT 1 FROM projects p WHERE p.tenant_id=t.id);

INSERT INTO collab_workspace_members(workspace_id, user_id, role, status)
SELECT w.id, m.user_id,
 CASE WHEN m.role='admin' THEN 'owner' ELSE 'member' END, 'active'
FROM collab_workspaces w
JOIN tenant_memberships m ON m.tenant_id=w.tenant_id AND m.status='active'
JOIN users u ON u.id=m.user_id AND u.status='active' AND u.deleted_at IS NULL;

ALTER TABLE projects ADD COLUMN space_id uuid;

UPDATE projects p SET space_id=(
 SELECT w.id FROM collab_workspaces w WHERE w.tenant_id=p.tenant_id AND w.slug='default'
);

-- The UPDATE queues deferred constraint triggers on projects; PostgreSQL refuses
-- further ALTER TABLE while trigger events are pending, so fire them now.
SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE projects ALTER COLUMN space_id SET NOT NULL;
ALTER TABLE projects ADD FOREIGN KEY(space_id,tenant_id) REFERENCES collab_workspaces(id,tenant_id);
CREATE INDEX project_space_list ON projects(space_id,id);
