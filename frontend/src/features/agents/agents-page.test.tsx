import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { installCloudSpaceHandlers, TEST_SPACE_ID, TEST_TENANT_ID } from '@/test/cloud-handlers'
import { renderWithProviders } from '@/test/render'
import { server } from '@/test/msw-server'
import { AgentsPage } from './agents-page'

const AGENTS_PATH = `/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/agents`

function cloudAgent(id: string, name: string, status: 'active' | 'disabled' = 'active') {
  return {
    id,
    workspaceId: TEST_SPACE_ID,
    name,
    status,
    version: 1,
    createdBy: 'u1',
    createdAt: '2026-09-20T10:00:00+08:00',
    updatedAt: '2026-09-20T10:00:00+08:00',
    deletedAt: null,
  }
}

describe('AgentsPage', () => {
  it('renders every agent of the resolved space', async () => {
    installCloudSpaceHandlers('member')
    server.use(
      http.get(AGENTS_PATH, () =>
        HttpResponse.json({
          items: [
            cloudAgent('11111111-1111-1111-1111-111111111111', '审查助手'),
            cloudAgent('22222222-2222-2222-2222-222222222222', '压测机器人', 'disabled'),
          ],
          nextCursor: '',
        }),
      ),
    )
    renderWithProviders(<AgentsPage slug="cloud-dev" />, { slug: 'cloud-dev' })

    expect(await screen.findByText('审查助手')).toBeInTheDocument()
    expect(await screen.findByText('压测机器人')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /新建智能体/ })).toBeInTheDocument()
  })

  it('offers no creation action until the space resolves', () => {
    renderWithProviders(<AgentsPage slug="cloud-dev" />, { slug: 'cloud-dev' })

    expect(screen.queryByRole('button', { name: /新建智能体/ })).not.toBeInTheDocument()
    expect(screen.queryByText('审查助手')).not.toBeInTheDocument()
  })
})
