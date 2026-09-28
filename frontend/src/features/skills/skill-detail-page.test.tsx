import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { installCloudSpaceHandlers, TEST_SPACE_ID, TEST_TENANT_ID } from '@/test/cloud-handlers'
import { renderAtRoute } from '@/test/render'
import { server } from '@/test/msw-server'
import { SkillDetailPage } from './skill-detail-page'

const SKILL_ID = '11111111-1111-1111-1111-111111111111'
const DIGEST = 'b'.repeat(64)

describe('SkillDetailPage', () => {
  it('shows the skill identity and full current revision digest', async () => {
    installCloudSpaceHandlers('member')
    server.use(
      http.get(`/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/skills/${SKILL_ID}`, () =>
        HttpResponse.json({
          id: SKILL_ID,
          workspaceId: TEST_SPACE_ID,
          canonicalName: 'web-search',
          displayName: '网页搜索',
          summary: '快速检索并汇总信息。',
          version: 2,
          createdAt: '2026-09-20T10:00:00+08:00',
          updatedAt: '2026-09-20T10:00:00+08:00',
          currentRevision: {
            id: 'rev-1',
            contentDigest: DIGEST,
            packageFormat: 'zip',
            packageFormatVersion: 1,
            sizeBytes: 4096,
          },
        }),
      ),
    )
    renderAtRoute(
      '/w/:workspaceSlug/skills/:skillId',
      <SkillDetailPage slug="cloud-dev" />,
      `/w/cloud-dev/skills/${SKILL_ID}`,
    )

    expect(await screen.findByText('网页搜索')).toBeInTheDocument()
    expect(screen.getByText('web-search')).toBeInTheDocument()
    expect(screen.getByText(DIGEST)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /上传新版本/ })).toBeInTheDocument()
  })
})
