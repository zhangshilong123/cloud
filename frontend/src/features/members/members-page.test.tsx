import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { MembersPage } from '@/features/members/members-page'
import { db } from '@/mocks/data/store'
import { installCloudSpaceHandlers, TEST_SPACE_ID, TEST_TENANT_ID } from '@/test/cloud-handlers'
import { renderWithProviders } from '@/test/render'
import { server } from '@/test/msw-server'

const ALICE_ID = '33333333-3333-3333-3333-333333333333'
const BOB_ID = '44444444-4444-4444-4444-444444444444'

function memberRow(id: string, displayName: string, role: string) {
  return {
    id,
    workspaceId: TEST_SPACE_ID,
    userId: id,
    role,
    status: 'active',
    version: 1,
    displayName,
    joinedAt: '2026-09-20T10:00:00+08:00',
  }
}

function installMembersHandler(members: unknown[]) {
  server.use(
    http.get(`/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/members`, () =>
      HttpResponse.json({ items: members, nextCursor: '' }),
    ),
  )
}

/** Renders the page for an owner session (Alice as owner) and waits for the list. */
function renderOwner() {
  installCloudSpaceHandlers('owner')
  installMembersHandler([memberRow(ALICE_ID, 'Alice', 'owner')])
  renderWithProviders(<MembersPage slug="cloud-dev" />, {
    slug: 'cloud-dev',
    authenticated: true,
  })
}

/** Types an email into the open add-member dialog and submits the add. */
async function typeAndSubmitEmail(
  user: ReturnType<typeof userEvent.setup>,
  email: string,
): Promise<void> {
  await user.type(screen.getByLabelText('成员邮箱'), email)
  await user.click(screen.getByRole('button', { name: '添加' }))
}

describe('MembersPage cloud mode', () => {
  it('renders real members and hides management controls from members', async () => {
    installCloudSpaceHandlers('member')
    installMembersHandler([
      memberRow(ALICE_ID, 'Alice', 'owner'),
      memberRow(BOB_ID, 'Bob', 'member'),
    ])
    renderWithProviders(<MembersPage slug="cloud-dev" />, {
      slug: 'cloud-dev',
      authenticated: true,
    })

    expect(await screen.findByText('Alice')).toBeInTheDocument()
    expect(await screen.findByText('Bob')).toBeInTheDocument()
    expect(screen.getByText('所有者')).toBeInTheDocument()
    // Members are read-only: no add trigger, no role selectors, no action column.
    expect(screen.queryByRole('button', { name: '添加成员' })).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Bob 的角色')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '禁用' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '移除' })).not.toBeInTheDocument()
  })

  it('lets an owner add a member by email through the API', async () => {
    renderOwner()
    const user = userEvent.setup()
    await screen.findByText('Alice')
    let postBody: unknown = null
    let idempotencyKey = ''
    server.use(
      http.post(`/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/members`, async ({ request }) => {
        postBody = await request.json()
        idempotencyKey = request.headers.get('Idempotency-Key') ?? ''
        return HttpResponse.json(memberRow(BOB_ID, 'Bob', 'member'))
      }),
    )

    await user.click(screen.getByRole('button', { name: '添加成员' }))
    expect(screen.getByLabelText('成员邮箱')).toBeInTheDocument()
    await typeAndSubmitEmail(user, 'bob@example.com')

    await waitFor(() => expect(postBody).not.toBeNull())
    expect(postBody).toEqual({ email: 'bob@example.com' })
    // POST must carry a non-empty idempotency key so retries dedupe.
    expect(idempotencyKey.length).toBeGreaterThan(0)
    // The dialog closes on success.
    await waitFor(() => expect(screen.queryByLabelText('成员邮箱')).not.toBeInTheDocument())
  })

  it('rejects an empty or malformed email with a local hint', async () => {
    renderOwner()
    const user = userEvent.setup()
    await screen.findByText('Alice')
    let posted = false
    server.use(
      http.post(`/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/members`, async () => {
        posted = true
        return HttpResponse.json(memberRow(BOB_ID, 'Bob', 'member'))
      }),
    )

    await user.click(screen.getByRole('button', { name: '添加成员' }))
    await user.click(screen.getByRole('button', { name: '添加' }))
    expect(await screen.findByText('请输入邮箱地址。')).toBeInTheDocument()
    expect(posted).toBe(false)

    await typeAndSubmitEmail(user, 'not-an-email')
    expect(await screen.findByText('请输入有效的邮箱地址。')).toBeInTheDocument()
    expect(posted).toBe(false)
  })

  it('shows the not-registered hint when the backend rejects an unknown email', async () => {
    renderOwner()
    const user = userEvent.setup()
    await screen.findByText('Alice')
    server.use(
      http.post(`/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/members`, () =>
        HttpResponse.json(
          { code: 'user_not_registered', params: {}, requestId: 'r' },
          { status: 404 },
        ),
      ),
    )

    await user.click(screen.getByRole('button', { name: '添加成员' }))
    await typeAndSubmitEmail(user, 'ghost@example.com')

    expect(await screen.findByText('该邮箱尚未注册，请先完成注册。')).toBeInTheDocument()
    // The dialog stays open so the actor can correct the address.
    expect(screen.getByLabelText('成员邮箱')).toBeInTheDocument()
  })

  it('shows role select and remove for the owner actor; owner rows are not removable', async () => {
    installCloudSpaceHandlers('owner')
    installMembersHandler([
      memberRow(ALICE_ID, 'Alice', 'owner'),
      memberRow(BOB_ID, 'Bob', 'member'),
    ])
    renderWithProviders(<MembersPage slug="cloud-dev" />, {
      slug: 'cloud-dev',
      authenticated: true,
    })
    await screen.findByText('Alice')

    // The member row gets a role selector and a remove action.
    expect(screen.getByLabelText('Bob 的角色')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '移除' })).toBeInTheDocument()
    // Owner rows keep the role selector (grant/demote via the API) but carry no
    // remove and no disable (backend last-owner invariant).
    expect(screen.getByLabelText('Alice 的角色')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: '移除' })).toHaveLength(1)
    expect(screen.getByRole('button', { name: '禁用' })).toBeDisabled()
  })

  it('keeps admin to add-only: add preserved, no remove, owner role not offered', async () => {
    installCloudSpaceHandlers('admin')
    installMembersHandler([
      memberRow(ALICE_ID, 'Alice', 'owner'),
      memberRow(BOB_ID, 'Bob', 'member'),
    ])
    const user = userEvent.setup()
    renderWithProviders(<MembersPage slug="cloud-dev" />, {
      slug: 'cloud-dev',
      authenticated: true,
    })
    await screen.findByText('Alice')

    expect(screen.getByRole('button', { name: '添加成员' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '移除' })).not.toBeInTheDocument()
    // Admin can change member roles but cannot grant owner.
    await user.click(screen.getByLabelText('Bob 的角色'))
    expect(await screen.findByText('管理员')).toBeInTheDocument()
    expect(screen.queryAllByText('所有者')).toHaveLength(0)
  })

  it('confirms membership-only removal before calling the DELETE member endpoint', async () => {
    installCloudSpaceHandlers('owner')
    installMembersHandler([
      memberRow(ALICE_ID, 'Alice', 'owner'),
      memberRow(BOB_ID, 'Bob', 'member'),
    ])
    const user = userEvent.setup()
    renderWithProviders(<MembersPage slug="cloud-dev" />, {
      slug: 'cloud-dev',
      authenticated: true,
    })
    await screen.findByText('Alice')
    let deleted = false
    let idempotencyKey = ''
    let bodyVersion: unknown
    server.use(
      http.delete(`/api/v1/tenants/${TEST_TENANT_ID}/spaces/${TEST_SPACE_ID}/members/${BOB_ID}`, async ({ request }) => {
        deleted = true
        idempotencyKey = request.headers.get('Idempotency-Key') ?? ''
        bodyVersion = (await request.clone().json())['version']
        return HttpResponse.json(memberRow(BOB_ID, 'Bob', 'member'))
      }),
    )

    await user.click(screen.getByRole('button', { name: '移除' }))
    expect(await screen.findByText('从工作区移除「Bob」？')).toBeInTheDocument()
    // The confirm copy explains workspace-membership-only removal, not account
    // or tenant-membership deletion, and that created resources remain.
    expect(screen.getByText(/账号与租户成员关系不受影响/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '确认移除' }))
    await waitFor(() => expect(deleted).toBe(true))
    // DELETE must carry a non-empty idempotency key so retries dedupe.
    expect(idempotencyKey.length).toBeGreaterThan(0)
    // The optimistic lock reads `version` from the JSON body (the router
    // requires a body on non-GET), so it must not be missing.
    expect(bodyVersion).toBe(1)
  })

  it('keeps the demo store table for mock sessions', async () => {
    renderWithProviders(<MembersPage slug={db.workspace.slug} />, { slug: db.workspace.slug })
    const first = db.users[0]
    if (!first) throw new Error('seed users must not be empty')
    expect(await screen.findByText(first.name)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '添加成员' })).not.toBeInTheDocument()
  })
})
