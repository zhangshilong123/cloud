import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { installCloudSpaceHandlers, TEST_SPACE_ID, TEST_TENANT_ID } from '@/test/cloud-handlers'
import { renderAtRoute } from '@/test/render'
import { server } from '@/test/msw-server'
import { SkillDetailPage } from './skill-detail-page'

const SKILL_ID = '11111111-1111-1111-1111-111111111111'
const DIGEST = 'b'.repeat(64)

/** The live Skill payload the detail page renders and the DELETE endpoint returns. */
function cloudSkill(version: number) {
  return {
    id: SKILL_ID,
    workspaceId: TEST_SPACE_ID,
    canonicalName: 'web-search',
    displayName: '网页搜索',
    summary: '快速检索并汇总信息。',
    version,
    createdAt: '2026-09-20T10:00:00+08:00',
    updatedAt: '2026-09-20T10:00:00+08:00',
    currentRevision: {
      id: 'rev-1',
      description: '联网检索并汇总网页信息。',
      contentDigest: DIGEST,
      packageFormat: 'zip',
      packageFormatVersion: 1,
      sizeBytes: 4096,
    },
  }
}

function renderDetail() {
  renderAtRoute(
    '/w/:workspaceSlug/skills/:skillId',
    <SkillDetailPage slug="cloud-dev" />,
    `/w/cloud-dev/skills/${SKILL_ID}`,
  )
}

describe('SkillDetailPage', () => {
  it('shows the package description and hides the content digest', async () => {
    installCloudSpaceHandlers('member')
    server.use(
      http.get(`/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/skills/${SKILL_ID}`, () =>
        HttpResponse.json(cloudSkill(2)),
      ),
    )
    renderDetail()

    expect(await screen.findByText('网页搜索')).toBeInTheDocument()
    expect(screen.getByText('web-search')).toBeInTheDocument()
    // Package description leads the body; the workspace summary follows as secondary metadata.
    expect(screen.getByText('联网检索并汇总网页信息。')).toBeInTheDocument()
    expect(screen.getByText(/工作区说明：快速检索并汇总信息/)).toBeInTheDocument()
    // Digest is machine metadata and must not be rendered.
    expect(screen.queryByText(DIGEST)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /上传新版本/ })).toBeInTheDocument()
  })

  it('deletes the skill after confirmation, carrying the version and an idempotency key', async () => {
    installCloudSpaceHandlers('member')
    let deleteBody: unknown = null
    let deleteKey: string | null = null
    server.use(
      http.get(`/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/skills/${SKILL_ID}`, () =>
        HttpResponse.json(cloudSkill(2)),
      ),
      http.delete(
        `/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/skills/${SKILL_ID}`,
        async ({ request }) => {
          deleteBody = await request.json()
          deleteKey = request.headers.get('Idempotency-Key')
          return HttpResponse.json(cloudSkill(3))
        },
      ),
    )
    renderDetail()
    const user = userEvent.setup()

    await screen.findByText('网页搜索')
    await user.click(screen.getByRole('button', { name: '删除技能' }))
    // The confirmation names the target and waits for an explicit confirm.
    expect(await screen.findByText('删除「网页搜索」？')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '确认删除' }))

    await waitFor(() => expect(deleteBody).not.toBeNull())
    expect(deleteBody).toEqual({ version: 2 })
    expect(deleteKey).not.toBe('')
  })

  it('surfaces a version conflict from delete inside the dialog', async () => {
    installCloudSpaceHandlers('member')
    server.use(
      http.get(`/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/skills/${SKILL_ID}`, () =>
        HttpResponse.json(cloudSkill(2)),
      ),
      http.delete(
        `/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/skills/${SKILL_ID}`,
        () =>
          HttpResponse.json(
            { code: 'version_conflict', params: {}, requestId: '' },
            { status: 409 },
          ),
      ),
    )
    renderDetail()
    const user = userEvent.setup()

    await screen.findByText('网页搜索')
    await user.click(screen.getByRole('button', { name: '删除技能' }))
    await user.click(await screen.findByRole('button', { name: '确认删除' }))

    expect(await screen.findByText(/删除失败：version_conflict/)).toBeInTheDocument()
  })
})
