import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { installCloudSpaceHandlers, TEST_SPACE_ID, TEST_TENANT_ID } from '@/test/cloud-handlers'
import { renderWithProviders } from '@/test/render'
import { server } from '@/test/msw-server'
import { ImportSkillDialog } from './import-skill-dialog'

/** Reads central-directory entry names out of a ZIP byte buffer. */
function zipEntryNames(buffer: ArrayBuffer): string[] {
  const view = new DataView(buffer)
  const bytes = new Uint8Array(buffer)
  const eocd = buffer.byteLength - 22
  const count = view.getUint16(eocd + 10, true)
  const centralOffset = view.getUint32(eocd + 16, true)
  const names: string[] = []
  let pos = centralOffset
  for (let i = 0; i < count; i += 1) {
    const nameLen = view.getUint16(pos + 28, true)
    const extraLen = view.getUint16(pos + 30, true)
    const commentLen = view.getUint16(pos + 32, true)
    names.push(new TextDecoder().decode(bytes.slice(pos + 46, pos + 46 + nameLen)))
    pos += 46 + nameLen + extraLen + commentLen
  }
  return names
}

/** The success envelope the folder-import tests assert on (one activated ingestion). */
const SINGLE_ACTIVATED_IMPORT = {
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
  ],
  preparationFailures: [],
}

/**
 * Installs the imports handler and records every request's multipart `source`
 * File plus per-request metadata, so folder-import tests share one capture
 * instead of repeating the formData/File plumbing.
 */
function installFolderImportCapture() {
  const capture = {
    file: null as File | null,
    requests: 0,
    sourceKinds: [] as (FormDataEntryValue | null)[],
    idempotencyKeys: [] as string[],
  }
  server.use(
    http.post(
      `/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/skills/imports`,
      async ({ request }) => {
        capture.requests += 1
        const form = await request.formData()
        capture.sourceKinds.push(form.get('source_kind'))
        capture.idempotencyKeys.push(request.headers.get('Idempotency-Key') ?? '')
        const source = form.get('source')
        if (source === null || typeof source === 'string') throw new Error('expected a File source')
        capture.file = source
        return HttpResponse.json(SINGLE_ACTIVATED_IMPORT)
      },
    ),
  )
  return capture
}

/** Renders the dialog, selects the folder tab, and returns a ready user-event handle. */
async function openFolderTab() {
  renderWithProviders(<ImportSkillDialog open onOpenChange={() => {}} />, { slug: 'cloud-dev' })
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: /^文件夹$/ }))
  return user
}

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

  it('packs a picked folder into one zip import request', async () => {
    installCloudSpaceHandlers('member')
    const capture = installFolderImportCapture()
    const user = await openFolderTab()

    const picked = new File(['<skill/>'], 'SKILL.md')
    Object.defineProperty(picked, 'webkitRelativePath', { value: 'MySkill/SKILL.md' })
    await user.upload(screen.getByLabelText('选择文件夹'), picked)

    expect(screen.getByText('1 个文件 · 将打包为单个 ZIP 上传')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /^导入$/ }))

    expect(await screen.findByText(/导入成功/)).toBeInTheDocument()
    expect(capture.idempotencyKeys).toHaveLength(1)
    expect(capture.sourceKinds).toEqual(['zip'])
    const archive = capture.file
    if (archive === null) throw new Error('expected a File')
    expect(archive.name).toBe('MySkill.zip')
    const head = new Uint8Array(await archive.slice(0, 4).arrayBuffer())
    expect(Array.from(head)).toEqual([0x50, 0x4b, 0x03, 0x04])
  })

  it('packs a parent folder with multiple Skills into one zip without reporting empty', async () => {
    installCloudSpaceHandlers('member')
    const capture = installFolderImportCapture()
    const user = await openFolderTab()

    const a = new File(['<a/>'], 'SKILL.md')
    Object.defineProperty(a, 'webkitRelativePath', { value: 'test/skill-a/SKILL.md' })
    const b = new File(['<b/>'], 'SKILL.md')
    Object.defineProperty(b, 'webkitRelativePath', { value: 'test/skill-b/SKILL.md' })
    await user.upload(screen.getByLabelText('选择文件夹'), [a, b])

    expect(screen.queryByText(/所选文件夹为空/)).not.toBeInTheDocument()
    expect(screen.getByText('2 个文件 · 将打包为单个 ZIP 上传')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /^导入$/ }))

    expect(await screen.findByText(/导入成功/)).toBeInTheDocument()
    expect(capture.requests).toBe(1)
    const archive = capture.file
    if (archive === null) throw new Error('expected a File')
    const names = zipEntryNames(await archive.arrayBuffer())
    expect(names).toEqual(['skill-a/SKILL.md', 'skill-b/SKILL.md'])
  })
})
