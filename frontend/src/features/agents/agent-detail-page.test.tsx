import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { installCloudSpaceHandlers, TEST_SPACE_ID, TEST_TENANT_ID } from '@/test/cloud-handlers'
import { renderAtRoute } from '@/test/render'
import { server } from '@/test/msw-server'
import { AgentDetailPage } from './agent-detail-page'

const AGENT_ID = '11111111-1111-1111-1111-111111111111'
const AGENT_PATH = `/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/agents/${AGENT_ID}`
const BINDINGS_PATH = `${AGENT_PATH}/skills`
const EXECUTIONS_PATH = `/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/executions`

function cloudAgent() {
  return {
    id: AGENT_ID,
    workspaceId: TEST_SPACE_ID,
    name: '审查助手',
    status: 'active',
    version: 3,
    createdBy: 'u1',
    createdAt: '2026-09-20T10:00:00+08:00',
    updatedAt: '2026-09-20T10:00:00+08:00',
    deletedAt: null,
  }
}

function binding(skillId: string, displayName: string, canonicalName: string) {
  return {
    id: `binding-${skillId}`,
    agentId: AGENT_ID,
    skillId,
    canonicalName,
    displayName,
    enabled: true,
    version: 1,
    createdAt: '2026-09-20T10:00:00+08:00',
    updatedAt: '2026-09-20T10:00:00+08:00',
  }
}

function executionRecord(executionId: string, agentId: string) {
  return {
    execution: {
      executionId,
      agentId,
      actorUserId: 'u1',
      input: {},
      tenantId: TEST_TENANT_ID,
      workspaceId: TEST_SPACE_ID,
      createdAt: '2026-09-28T10:00:00+08:00',
    },
    attempts: [
      {
        attemptId: 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa',
        executionId,
        ordinal: 1,
        state: 'eligible',
        createdAt: '2026-09-28T10:00:00+08:00',
        updatedAt: '2026-09-28T10:00:00+08:00',
        nodeId: null,
        dispatchedEpoch: null,
        result: null,
      },
    ],
    skillBindings: [
      {
        executionId,
        skillId: 'skill-1',
        skillRevisionId: 'rev-1',
        canonicalName: 'web-search',
        contentDigest: 'c'.repeat(64),
        packageFormat: 'zip',
        packageFormatVersion: 1,
        sizeBytes: 2048,
        createdAt: '2026-09-28T10:00:00+08:00',
      },
    ],
  }
}

function installAgentFixtures() {
  installCloudSpaceHandlers('member')
  server.use(
    http.get(AGENT_PATH, () => HttpResponse.json(cloudAgent())),
    http.get(BINDINGS_PATH, () =>
      HttpResponse.json({
        items: [binding('skill-1', '网页搜索', 'web-search')],
        nextCursor: '',
      }),
    ),
  )
}

describe('AgentDetailPage', () => {
  it('renders the agent identity and its assigned skill bindings', async () => {
    installAgentFixtures()
    renderAtRoute(
      '/w/:workspaceSlug/agents/:agentId',
      <AgentDetailPage slug="cloud-dev" />,
      `/w/cloud-dev/agents/${AGENT_ID}`,
    )

    expect(await screen.findByText('审查助手')).toBeInTheDocument()
    expect(await screen.findByText('网页搜索')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /创建 Execution/ })).toBeInTheDocument()
  })

  it('admits an execution carrying only the agent identity and shows the frozen snapshot', async () => {
    installAgentFixtures()
    let admittedBody: unknown
    server.use(
      http.post(EXECUTIONS_PATH, async ({ request }) => {
        admittedBody = await request.json()
        return HttpResponse.json(executionRecord('exec-1', AGENT_ID))
      }),
      http.get(`${EXECUTIONS_PATH}/exec-1`, () =>
        HttpResponse.json(executionRecord('exec-1', AGENT_ID)),
      ),
    )

    const user = userEvent.setup()
    renderAtRoute(
      '/w/:workspaceSlug/agents/:agentId',
      <AgentDetailPage slug="cloud-dev" />,
      `/w/cloud-dev/agents/${AGENT_ID}`,
    )

    await user.click(await screen.findByRole('button', { name: /创建 Execution/ }))

    expect(await screen.findByText(/此 Execution 已冻结/)).toBeInTheDocument()
    expect(screen.getByText(/等待运行时投递/)).toBeInTheDocument()
    expect(screen.getByText('web-search')).toBeInTheDocument()
    await waitFor(() => expect(admittedBody).toEqual({ agentId: AGENT_ID }))
  })
})
