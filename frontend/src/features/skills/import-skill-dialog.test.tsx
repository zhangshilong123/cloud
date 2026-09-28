import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { installCloudSpaceHandlers, TEST_SPACE_ID, TEST_TENANT_ID } from '@/test/cloud-handlers'
import { renderWithProviders } from '@/test/render'
import { server } from '@/test/msw-server'
import { ImportSkillDialog } from './import-skill-dialog'

describe('ImportSkillDialog', () => {
  it('imports a zip and distinguishes partial success from a clean one', async () => {
    installCloudSpaceHandlers('member')
    server.use(
      http.post(`/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/skills/imports`, () =>
        HttpResponse.json({
          sourceKind: 'zip',
          ingestions: [
            {
              canonicalName: 'web-search',
              activation: 'activated',
              state: 'committed',
              errorCode: '',
              candidateRoot: '',
              skillId: '11111111-1111-1111-1111-111111111111',
              revisionId: 'rev-1',
              ingestionId: null,
              replayed: false,
            },
            {
              canonicalName: 'web-search',
              activation: 'activation_conflict',
              state: 'committed',
              errorCode: '',
              candidateRoot: '',
              skillId: null,
              revisionId: null,
              ingestionId: null,
              replayed: false,
            },
          ],
          preparationFailures: [],
        }),
      ),
    )

    renderWithProviders(<ImportSkillDialog open onOpenChange={() => {}} />, {
      slug: 'cloud-dev',
    })

    const user = userEvent.setup()
    const file = new File(['<skill/>'], 'skill.zip', { type: 'application/zip' })
    await user.upload(screen.getByLabelText('归档文件'), file)
    await user.click(screen.getByRole('button', { name: /^导入$/ }))

    expect(await screen.findByText(/部分成功/)).toBeInTheDocument()
    expect(screen.getByText(/已存在同名 Skill/)).toBeInTheDocument()
  })
})
