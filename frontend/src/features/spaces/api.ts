import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { Error as ApiError, Space, SpaceListItem, SpaceMember } from '@/api/generated.schemas'
import {
  deleteApiV1TenantsTidSpacesSid,
  getApiV1TenantsTidSpaces,
  getApiV1TenantsTidSpacesSidMembers,
  getApiV1TenantsTidSpacesSidProjects,
  getGetApiV1TenantsTidSpacesQueryKey,
  patchApiV1TenantsTidSpacesSid,
  postApiV1TenantsTidSpaces,
  putApiV1TenantsTidSpacesSidMembersUid,
} from '@/api/spaces/spaces'
import type { ErrorType } from '@/lib/api-client'

/** Space membership roles; mirrors the backend owner/admin/member model. */
export type SpaceRole = 'owner' | 'admin' | 'member'

/**
 * Narrows the cloud's free-form role string to the UI union, falling back to
 * member for unknown values so role checks always render a known label.
 */
export function normalizeSpaceRole(role: string): SpaceRole {
  if (role === 'owner' || role === 'admin' || role === 'member') return role
  return 'member'
}

export type SpaceMemberStatus = 'active' | 'disabled'

/** Input for creating a collaboration space; slug is lowercase and immutable. */
export interface CreateSpaceInput {
  name: string
  slug: string
  description: string
}

/** Input for updating space settings; version guards concurrent edits. */
export interface UpdateSpaceInput {
  name: string
  description: string
  version: number
}

/** Input for upserting one membership; new members use version 0. */
export interface UpdateSpaceMemberInput {
  userId: string
  role: SpaceRole
  status: SpaceMemberStatus
  version: number
}

/**
 * Space API hooks wrap the generated orval client so pages talk domain terms
 * instead of endpoint paths. Mutations invalidate the affected lists on
 * success. All functions are disabled until a tenant id is known.
 */

export function useSpaces(tenantId: string | undefined) {
  return useQuery({
    queryKey: tenantId ? getGetApiV1TenantsTidSpacesQueryKey(tenantId) : ['spaces', 'no-tenant'],
    queryFn: ({ signal }) => getApiV1TenantsTidSpaces(tenantId ?? '', undefined, undefined, signal),
    enabled: !!tenantId,
  })
}

export function useSpaceMembers(tenantId: string | undefined, spaceId: string | undefined) {
  return useQuery({
    queryKey:
      tenantId && spaceId
        ? [`/api/v1/tenants/${tenantId}/spaces/${spaceId}/members`]
        : ['space-members', 'disabled'],
    queryFn: ({ signal }) =>
      getApiV1TenantsTidSpacesSidMembers(
        tenantId ?? '',
        spaceId ?? '',
        undefined,
        undefined,
        signal,
      ),
    enabled: !!tenantId && !!spaceId,
  })
}

export function useSpaceProjects(tenantId: string | undefined, spaceId: string | undefined) {
  return useQuery({
    queryKey:
      tenantId && spaceId
        ? [`/api/v1/tenants/${tenantId}/spaces/${spaceId}/projects`]
        : ['space-projects', 'disabled'],
    queryFn: ({ signal }) =>
      getApiV1TenantsTidSpacesSidProjects(
        tenantId ?? '',
        spaceId ?? '',
        undefined,
        undefined,
        signal,
      ),
    enabled: !!tenantId && !!spaceId,
  })
}

export function useCreateSpace(tenantId: string | undefined) {
  const queryClient = useQueryClient()
  return useMutation<Space, ErrorType<ApiError>, CreateSpaceInput>({
    mutationFn: (input: CreateSpaceInput) => postApiV1TenantsTidSpaces(tenantId ?? '', input),
    onSuccess: () => {
      if (!tenantId) return
      void queryClient.invalidateQueries({
        queryKey: getGetApiV1TenantsTidSpacesQueryKey(tenantId),
      })
    },
  })
}

export function useUpdateSpace(tenantId: string | undefined, spaceId: string | undefined) {
  const queryClient = useQueryClient()
  return useMutation<Space, ErrorType<ApiError>, UpdateSpaceInput>({
    mutationFn: (input: UpdateSpaceInput) =>
      patchApiV1TenantsTidSpacesSid(tenantId ?? '', spaceId ?? '', input),
    onSuccess: (space: Space) => {
      if (!tenantId) return
      void queryClient.invalidateQueries({
        queryKey: getGetApiV1TenantsTidSpacesQueryKey(tenantId),
      })
      void queryClient.invalidateQueries({
        queryKey: [`/api/v1/tenants/${tenantId}/spaces/${space.id}`],
      })
    },
  })
}

export function useArchiveSpace(tenantId: string | undefined, spaceId: string | undefined) {
  const queryClient = useQueryClient()
  return useMutation<Space, ErrorType<ApiError>, number>({
    mutationFn: (version: number) =>
      deleteApiV1TenantsTidSpacesSid(tenantId ?? '', spaceId ?? '', { version }),
    onSuccess: () => {
      if (!tenantId) return
      void queryClient.invalidateQueries({
        queryKey: getGetApiV1TenantsTidSpacesQueryKey(tenantId),
      })
    },
  })
}

export function useUpdateSpaceMember(tenantId: string | undefined, spaceId: string | undefined) {
  const queryClient = useQueryClient()
  return useMutation<SpaceMember, ErrorType<ApiError>, UpdateSpaceMemberInput>({
    mutationFn: (input: UpdateSpaceMemberInput) =>
      putApiV1TenantsTidSpacesSidMembersUid(tenantId ?? '', spaceId ?? '', input.userId, {
        role: input.role,
        status: input.status,
        version: input.version,
      }),
    onSuccess: () => {
      if (!tenantId || !spaceId) return
      void queryClient.invalidateQueries({
        queryKey: [`/api/v1/tenants/${tenantId}/spaces/${spaceId}/members`],
      })
    },
  })
}

export type { Space, SpaceListItem }
