import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server } from '@/test/msw-server'
import type { Project, Space } from '@/api/generated.schemas'
import { useArchiveSpace, useCreateSpace, useSpaceProjects } from './api'

const TENANT_ID = '11111111-1111-1111-1111-111111111111'
const SPACE_ID = '22222222-2222-2222-2222-222222222222'
const CREATE_URL = `/api/v1/tenants/${TENANT_ID}/spaces`
const ARCHIVE_URL = `/api/v1/tenants/${TENANT_ID}/spaces/${SPACE_ID}`
const CREATE_INPUT = { name: 'Team Space', slug: 'team', description: '' }

/** The headers one request arrived with. */
interface Recorded {
  key: string | null
  contentType: string | null
}

function wrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
}

/**
 * A client whose mutations retry once, immediately. React Query reads `retry`
 * from the mutation options, and the client's defaults are merged into them,
 * which is what makes a failed attempt re-enter `mutationFn` — the exact path
 * the idempotency key has to survive.
 */
function retryingClient(): QueryClient {
  return new QueryClient({ defaultOptions: { mutations: { retry: 1, retryDelay: 0 } } })
}

/** Mounts the create hook; pass a client to control how mutations retry. */
function mountCreate(client = new QueryClient()) {
  return renderHook(() => useCreateSpace(TENANT_ID), { wrapper: wrapper(client) })
}

/** Mounts the archive hook for SPACE_ID; `client` works as in mountCreate. */
function mountArchive(client = new QueryClient()) {
  return renderHook(() => useArchiveSpace(TENANT_ID, SPACE_ID), { wrapper: wrapper(client) })
}

/** MSW handler for one space endpoint that records every request's headers. */
function spaceEndpoint(
  method: 'post' | 'delete',
  url: string,
  seen: Recorded[],
  reply: () => Response,
) {
  const resolver = ({ request }: { request: Request }) => {
    seen.push({
      key: request.headers.get('Idempotency-Key'),
      contentType: request.headers.get('Content-Type'),
    })
    return reply()
  }
  return method === 'post' ? http.post(url, resolver) : http.delete(url, resolver)
}

/** Replies with a space, failing the first `failures` attempts when asked. */
function spaceReply(failures = 0): () => Response {
  let attempts = 0
  return () => {
    attempts += 1
    if (attempts <= failures) return new HttpResponse(null, { status: 500 })
    return HttpResponse.json(spaceFixture())
  }
}

/** Asserts the first recorded request carried a usable key and the JSON type. */
function expectKeyed(seen: Recorded[]): void {
  expect(seen[0]?.key).toBeTruthy()
  expect(seen[0]?.key).not.toBe('')
  expect(seen[0]?.contentType).toContain('application/json')
}

/**
 * Runs one mutation that fails once then succeeds, recording both requests.
 * Asserts each attempt carried a non-empty key and that the retry minted a
 * fresh one — the shared client keys every request independently.
 */
async function expectFreshKeyPerRetry(
  method: 'post' | 'delete',
  url: string,
  result: { current: { isSuccess: boolean } },
  trigger: () => void,
): Promise<Recorded[]> {
  const seen: Recorded[] = []
  server.use(spaceEndpoint(method, url, seen, spaceReply(1)))
  trigger()
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(seen).toHaveLength(2)
  expect(seen[0]?.key).toBeTruthy()
  expect(seen[1]?.key).toBeTruthy()
  expect(seen[1]?.key).not.toBe(seen[0]?.key)
  expectKeyed(seen)
  return seen
}

function spaceFixture(overrides: Partial<Space> = {}): Space {
  return {
    id: SPACE_ID,
    tenantId: TENANT_ID,
    name: 'Team Space',
    slug: 'team',
    description: '',
    createdBy: 'u1',
    version: 1,
    createdAt: '2026-09-21T10:00:00+08:00',
    updatedAt: '2026-09-21T10:00:00+08:00',
    archivedAt: null,
    ...overrides,
  }
}

describe('useCreateSpace', () => {
  it('sends a non-empty key, keeps JSON, and mints a fresh key per mutate() call', async () => {
    const seen: Recorded[] = []
    server.use(spaceEndpoint('post', CREATE_URL, seen, spaceReply()))
    const { result } = mountCreate()

    result.current.mutate(CREATE_INPUT)

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(seen).toHaveLength(1)
    expectKeyed(seen)
    result.current.mutate({ name: 'Second', slug: 'second', description: '' })
    await waitFor(() => expect(seen).toHaveLength(2))
    expectKeyed(seen)
    expect(seen[1]?.key).toBeTruthy()
    expect(seen[1]?.key).not.toBe(seen[0]?.key)
  })

  it('mints a fresh Idempotency-Key on each retry attempt of the same mutation', async () => {
    const { result } = mountCreate(retryingClient())
    const seen = await expectFreshKeyPerRetry('post', CREATE_URL, result, () =>
      result.current.mutate(CREATE_INPUT),
    )
    expect(seen).toHaveLength(2)
  })
})

describe('useSpaceProjects workspace scoping', () => {
  const SPACE_A = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'
  const SPACE_B = 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb'

  function projectFixture(spaceId: string): Project {
    return {
      id: 'pppppppp-pppp-pppp-pppp-pppppppppppp',
      tenantId: TENANT_ID,
      name: `Project in ${spaceId}`,
      repositoryUrl: 'https://example.com/repo.git',
      defaultBranch: 'main',
      ownerUserId: 'u1',
      spaceId,
      lifecycle: 'active',
      version: 1,
      createdAt: '2026-09-21T10:00:00+08:00',
      deletedAt: null,
      credentialRefId: null,
    }
  }

  it('keys each space project list by space id and never crosses spaces', async () => {
    server.use(
      http.get(`/api/v1/tenants/${TENANT_ID}/spaces/${SPACE_A}/projects`, () =>
        HttpResponse.json({ items: [projectFixture(SPACE_A)], nextCursor: '' }),
      ),
      http.get(`/api/v1/tenants/${TENANT_ID}/spaces/${SPACE_B}/projects`, () =>
        HttpResponse.json({ items: [projectFixture(SPACE_B)], nextCursor: '' }),
      ),
    )
    const queryClient = new QueryClient()
    const { result, rerender } = renderHook(
      ({ spaceId }: { spaceId: string | undefined }) => useSpaceProjects(TENANT_ID, spaceId),
      { wrapper: wrapper(queryClient), initialProps: { spaceId: SPACE_A } },
    )

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.items[0]?.name).toContain(SPACE_A)

    // Switch the active workspace: the query is re-keyed to B, and the cached
    // A list must not leak into B's result.
    rerender({ spaceId: SPACE_B })
    await waitFor(() => expect(result.current.data?.items[0]?.name).toContain(SPACE_B))
    expect(result.current.data?.items[0]?.name).not.toContain(SPACE_A)
    expect(
      queryClient.getQueryData([`/api/v1/tenants/${TENANT_ID}/spaces/${SPACE_A}/projects`]),
    ).toBeTruthy()
    expect(
      queryClient.getQueryData([`/api/v1/tenants/${TENANT_ID}/spaces/${SPACE_B}/projects`]),
    ).toBeTruthy()
  })
})

describe('useArchiveSpace', () => {
  it('sends a non-empty Idempotency-Key on DELETE', async () => {
    const seen: Recorded[] = []
    server.use(spaceEndpoint('delete', ARCHIVE_URL, seen, spaceReply()))
    const { result } = mountArchive()

    result.current.mutate(1)

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(seen).toHaveLength(1)
    expectKeyed(seen)
  })

  it('mints a fresh Idempotency-Key on each retry attempt of the same archive', async () => {
    const { result } = mountArchive(retryingClient())
    const seen = await expectFreshKeyPerRetry('delete', ARCHIVE_URL, result, () =>
      result.current.mutate(1),
    )
    expect(seen).toHaveLength(2)
  })
})
