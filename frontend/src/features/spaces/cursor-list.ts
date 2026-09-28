import { useInfiniteQuery } from '@tanstack/react-query'
import { useCurrentSpace } from '@/features/spaces/current-space'

const PAGE_SIZE = 50

/** One page of a space-scoped cloud list, as every cursor endpoint returns. */
export interface CursorPage<T> {
  items: T[]
  nextCursor: string
}

/** Query subset shared by the space-scoped cursor list endpoints. */
export interface CursorListParams {
  limit: number
  after?: string
}

type Fetcher<T> = (
  tenantId: string,
  spaceId: string,
  params: CursorListParams,
  signal?: AbortSignal,
) => Promise<CursorPage<T>>

/**
 * Shared query for the space-scoped cursor-paginated list endpoints (agents,
 * skills): resolves tenant/space from the route slug, then walks the `after`
 * cursor. Disabled until the slug resolves to a joined space, so the list is
 * pending — never demo data — before then.
 */
export function useCursorList<T>(
  slug: string,
  disabledKey: string,
  buildKey: (tenantId: string, spaceId: string) => string,
  fetcher: Fetcher<T>,
) {
  const { tenantId, space } = useCurrentSpace()
  const spaceId = space?.slug === slug ? space.id : undefined
  return useInfiniteQuery({
    queryKey: tenantId && spaceId ? [buildKey(tenantId, spaceId)] : [disabledKey],
    queryFn: ({ pageParam, signal }) =>
      fetcher(
        tenantId ?? '',
        spaceId ?? '',
        { limit: PAGE_SIZE, ...(pageParam ? { after: pageParam } : {}) },
        signal,
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last): string | undefined => last.nextCursor || undefined,
    enabled: !!tenantId && !!spaceId,
  })
}
