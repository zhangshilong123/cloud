import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { installCloudSpaceHandlers, TEST_SPACE_ID, TEST_TENANT_ID } from '@/test/cloud-handlers'
import { renderWithProviders } from '@/test/render'
import { server } from '@/test/msw-server'
import { SkillsPage } from './skills-page'

const SKILLS_PATH = `/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/skills`

function cloudSkill(id: string, displayName: string) {
  return {
    id,
    workspaceId: TEST_SPACE_ID,
    canonicalName: displayName.toLowerCase().replace(/\s+/g, '-'),
    displayName,
    summary: '一个演示 Skill。',
    version: 1,
    createdAt: '2026-09-20T10:00:00+08:00',
    updatedAt: '2026-09-20T10:00:00+08:00',
    currentRevision: {
      id: `rev-${id}`,
      contentDigest: 'a'.repeat(64),
      packageFormat: 'zip',
      packageFormatVersion: 1,
      sizeBytes: 2048,
    },
  }
}

describe('SkillsPage', () => {
  it('renders every skill of the resolved space', async () => {
    installCloudSpaceHandlers('member')
    server.use(
      http.get(SKILLS_PATH, () =>
        HttpResponse.json({
          items: [
            cloudSkill('11111111-1111-1111-1111-111111111111', '网页搜索'),
            cloudSkill('22222222-2222-2222-2222-222222222222', '代码评审'),
          ],
          nextCursor: '',
        }),
      ),
    )
    renderWithProviders(<SkillsPage slug="cloud-dev" />, { slug: 'cloud-dev' })

    expect(await screen.findByText('网页搜索')).toBeInTheDocument()
    expect(await screen.findByText('代码评审')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /导入技能/ })).toBeInTheDocument()
  })

  it('offers no import action until the space resolves', () => {
    renderWithProviders(<SkillsPage slug="cloud-dev" />, { slug: 'cloud-dev' })

    expect(screen.queryByRole('button', { name: /导入技能/ })).not.toBeInTheDocument()
    expect(screen.queryByText('网页搜索')).not.toBeInTheDocument()
  })
})
