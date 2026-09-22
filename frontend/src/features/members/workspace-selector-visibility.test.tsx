import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { DashboardLayout } from '@/components/layout/dashboard-layout'
import type { SpaceListItem } from '@/api/generated.schemas'
import { TEST_SPACE_ID, TEST_TENANT_ID } from '@/test/cloud-handlers'
import { server } from '@/test/msw-server'

const TEAM_SLUG = 'team'
const NEW_SLUG = 'platform'

/** Gateway current-user fact; the cookie session is represented by this only. */
const currentUser = {
  id: '00000000-0000-4000-8000-000000000001',
  displayName: 'Alice',
  status: 'active',
  version: 1,
  createdAt: '2026-09-21T00:00:00Z',
  deletedAt: null,
}

function spaceItem(id: string, name: string, slug: string, role: string): SpaceListItem {
  return {
    id,
    tenantId: TEST_TENANT_ID,
    name,
    slug,
    description: '',
    role,
    createdBy: 'u1',
    version: 1,
    createdAt: '2026-09-21T10:00:00+08:00',
    updatedAt: '2026-09-21T10:00:00+08:00',
    archivedAt: null,
  }
}

/** One render = one page load; a fresh QueryClient models a refresh/re-enter. */
function renderDashboard(initialPath: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createMemoryRouter(
    [
      { path: '/login', element: <div>Login screen</div> },
      {
        path: '/:workspaceSlug',
        element: <DashboardLayout />,
        children: [
          { path: 'issues', element: <div>Issues screen</div> },
          { path: 'projects', element: <div>Projects screen</div> },
        ],
      },
    ],
    { initialEntries: [initialPath] },
  )
  return render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

async function openSwitcher(name: string) {
  const user = userEvent.setup()
  // The trigger's accessible name is the active workspace label; wait for the
  // spaces query to resolve so the selector reflects the current membership.
  await waitFor(() =>
    expect(screen.getByRole('button', { name: new RegExp(name) })).toBeInTheDocument(),
  )
  await user.click(screen.getByRole('button', { name: new RegExp(name) }))
  return screen.findByRole('menu')
}

describe('Workspace selector visibility for a newly enrolled member', () => {
  // The Gateway cookie is represented by a resolved /api/v1/me; the tenant and
  // the mutable spaces list drive what the selector may list.
  beforeEach(() => {
    server.use(
      http.get('/api/v1/me', () => HttpResponse.json(currentUser)),
      http.get('/api/v1/me/tenants', () =>
        HttpResponse.json({
          items: [{ id: TEST_TENANT_ID, name: '研发组织', status: 'active', role: 'admin' }],
          nextCursor: '',
        }),
      ),
    )
  })

  it('does not list a workspace before enrollment, then shows and switches to it after refresh', async () => {
    const user = userEvent.setup()
    // First load: B joined only the default/team space; the newly-added W is absent.
    let spaces: SpaceListItem[] = [spaceItem(TEST_SPACE_ID, 'Team Space', TEAM_SLUG, 'member')]
    server.use(
      http.get(`/api/v1/tenants/${TEST_TENANT_ID}/spaces`, () =>
        HttpResponse.json({ items: spaces, nextCursor: '' }),
      ),
    )

    const first = renderDashboard(`/${TEAM_SLUG}/projects`)
    await screen.findByText('Projects screen')
    await openSwitcher('Team Space')
    expect(screen.queryByRole('menuitem', { name: new RegExp('Platform') })).not.toBeInTheDocument()
    first.unmount()

    // A refresh/re-enter is a fresh page load: the backend now lists W because B
    // was enrolled. The in-memory query cache does not survive the reload, so the
    // selector picks up the newly visible workspace with no manual cache clear.
    spaces = [
      spaceItem(TEST_SPACE_ID, 'Team Space', TEAM_SLUG, 'member'),
      spaceItem('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa', 'Platform', NEW_SLUG, 'member'),
    ]
    renderDashboard(`/${TEAM_SLUG}/projects`)
    await screen.findByText('Projects screen')
    await openSwitcher('Team Space')

    const item = await screen.findByRole('menuitem', { name: new RegExp('Platform') })
    expect(item).toBeInTheDocument()
    await user.click(item)

    await waitFor(() => {
      expect(screen.getByText('Projects screen')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: new RegExp('Platform') })).toBeInTheDocument()
    })
  })
})
