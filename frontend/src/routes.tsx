import type { ComponentType } from 'react'
import { createBrowserRouter, Navigate, useParams } from 'react-router-dom'
import { DashboardLayout } from '@/components/layout/dashboard-layout'
import { AgentDetailPage } from '@/features/agents/agent-detail-page'
import { AgentsPage } from '@/features/agents/agents-page'
import { LoginPage } from '@/features/auth/login-page'
import { BillingPage } from '@/features/billing/billing-page'
import { ChatPage } from '@/features/chat/chat-page'
import { InboxPage } from '@/features/inbox/inbox-page'
import { IssueDetailPage } from '@/features/issues/issue-detail-page'
import { IssuesPage } from '@/features/issues/issues-page'
import { MembersPage } from '@/features/members/members-page'
import { MyIssuesPage } from '@/features/my-issues/my-issues-page'
import { ProjectDetailPage } from '@/features/projects/project-detail-page'
import { ProjectsPage } from '@/features/projects/projects-page'
import { RuntimesPage } from '@/features/runtimes/runtimes-page'
import { GeneralSettingsPage } from '@/features/settings/general-settings-page'
import { SettingsLayout } from '@/features/settings/settings-layout'
import { SkillsPage } from '@/features/skills/skills-page'
import { SquadDetailPage } from '@/features/squads/squad-detail-page'
import { SquadsPage } from '@/features/squads/squads-page'
import { useCurrentSpace } from '@/features/spaces/current-space'
import { db } from '@/mocks/data/store'

/**
 * DashboardLayout only renders its children once :workspaceSlug matches a real
 * workspace, so every page under it can trust the param. Demo/preview pages
 * forward the space slug; real-backend pages are wrapped in {@link CloudScope},
 * which forwards the resolved tenant id instead.
 */
function WithSlug({ component: Component }: { component: ComponentType<{ slug: string }> }) {
  const { workspaceSlug } = useParams<{ workspaceSlug: string }>()
  if (!workspaceSlug) throw new Error('workspace route must provide a slug')
  return <Component slug={workspaceSlug} />
}

/**
 * Resolves the current collaboration space to its tenant id and forwards it as
 * the `slug` prop, so tenant-scoped real-backend pages (Issues, members, …) can
 * keep their `{ slug }` contract. In demo mode there is no tenant; the demo
 * plane has no real Issues, so redirect to its own issue board.
 */
function CloudScope({ component: Component }: { component: ComponentType<{ slug: string }> }) {
  const { tenantId } = useCurrentSpace()
  if (!tenantId) return <Navigate to={`/${db.workspace.slug}/issues`} replace />
  return <Component slug={tenantId} />
}

export const router = createBrowserRouter([
  // Authentication is verified by DashboardLayout against /api/v1/me: an
  // unknown root resolves to the demo workspace and is redirected to /login or
  // to the signed-in member's first real space there.
  { path: '/', element: <Navigate to={`/${db.workspace.slug}/issues`} replace /> },
  { path: '/login', element: <LoginPage /> },
  {
    path: '/:workspaceSlug',
    element: <DashboardLayout />,
    children: [
      { index: true, element: <Navigate to="issues" replace /> },
      { path: 'issues', element: <CloudScope component={IssuesPage} /> },
      { path: 'issues/:issueId', element: <CloudScope component={IssueDetailPage} /> },
      { path: 'my-issues', element: <CloudScope component={MyIssuesPage} /> },
      { path: 'projects', element: <WithSlug component={ProjectsPage} /> },
      { path: 'projects/:projectId', element: <WithSlug component={ProjectDetailPage} /> },
      { path: 'squads', element: <WithSlug component={SquadsPage} /> },
      { path: 'squads/:squadId', element: <WithSlug component={SquadDetailPage} /> },
      { path: 'agents', element: <WithSlug component={AgentsPage} /> },
      { path: 'agents/:agentId', element: <WithSlug component={AgentDetailPage} /> },
      { path: 'skills', element: <WithSlug component={SkillsPage} /> },
      { path: 'runtimes', element: <WithSlug component={RuntimesPage} /> },
      { path: 'chat', element: <WithSlug component={ChatPage} /> },
      { path: 'chat/:sessionId', element: <WithSlug component={ChatPage} /> },
      { path: 'inbox', element: <WithSlug component={InboxPage} /> },
      {
        path: 'settings',
        element: <WithSlug component={SettingsLayout} />,
        children: [
          { index: true, element: <GeneralSettingsPage /> },
          { path: 'members', element: <WithSlug component={MembersPage} /> },
          { path: 'billing', element: <WithSlug component={BillingPage} /> },
        ],
      },
    ],
  },
  { path: '*', element: <Navigate to="/" replace /> },
])
