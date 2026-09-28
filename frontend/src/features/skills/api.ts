import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { Error as ApiError, Skill, SourceUploadResult } from '@/api/generated.schemas'
import {
  deleteApiV1TenantsTidSpacesSpaceIdSkillsSkillId,
  getApiV1TenantsTidSpacesSpaceIdSkills,
  getApiV1TenantsTidSpacesSpaceIdSkillsSkillId,
  postApiV1TenantsTidSpacesSpaceIdSkillsImports,
} from '@/api/skills/skills'
import { useCurrentSpace } from '@/features/spaces/current-space'
import { mutationHeaders, useIdempotencyKeys } from '@/features/spaces/api'
import { useCursorList } from '@/features/spaces/cursor-list'
import type { ErrorType } from '@/lib/api-client'

function skillsKey(tenantId: string, spaceId: string): string {
  return `/api/v1/tenants/${tenantId}/spaces/${spaceId}/skills`
}

/**
 * Live Skills of the current space, cursor-paginated through the shared
 * space-scoped list query. Disabled (pending) until a tenant and space id are
 * known.
 */
export function useSkills(slug: string) {
  return useCursorList<Skill>(slug, 'skills', skillsKey, (tenantId, spaceId, params, signal) =>
    getApiV1TenantsTidSpacesSpaceIdSkills(tenantId, spaceId, params, undefined, signal),
  )
}

/** One live Skill by id, used by the detail page. */
export function useSkill(slug: string, skillId: string | undefined) {
  const { tenantId, space } = useCurrentSpace()
  const spaceId = space?.slug === slug ? space.id : undefined
  return useQuery({
    queryKey:
      tenantId && spaceId && skillId
        ? [`/api/v1/tenants/${tenantId}/spaces/${spaceId}/skills/${skillId}`]
        : ['skill', 'disabled'],
    queryFn: ({ signal }) =>
      getApiV1TenantsTidSpacesSpaceIdSkillsSkillId(
        tenantId ?? '',
        spaceId ?? '',
        skillId ?? '',
        undefined,
        signal,
      ),
    enabled: !!tenantId && !!spaceId && !!skillId,
  })
}

/** One user-selected archive submission for the import endpoint. */
export interface ImportSkillInput {
  displayName?: string
  summary?: string
  sourceKind: 'zip' | 'tar'
  source: File
  targetSkillId?: string
}

/**
 * Imports one Skill source (zip or uncompressed tar) through the multipart
 * import endpoint. The Idempotency-Key is minted once per submission — the
 * mutation retries with the same variables object, so a network retry replays
 * the same saga instead of creating a duplicate import.
 */
export function useImportSkill() {
  const queryClient = useQueryClient()
  const { tenantId, space } = useCurrentSpace()
  const keyFor = useIdempotencyKeys()
  return useMutation<SourceUploadResult, ErrorType<ApiError>, ImportSkillInput>({
    mutationFn: (input) => {
      if (!tenantId || !space) throw new Error('cloud space not resolved')
      return postApiV1TenantsTidSpacesSpaceIdSkillsImports(
        tenantId,
        space.id,
        {
          source_kind: input.sourceKind,
          source: input.source,
          ...(input.displayName !== undefined ? { display_name: input.displayName } : {}),
          ...(input.summary !== undefined ? { summary: input.summary } : {}),
          ...(input.targetSkillId !== undefined ? { target_skill_id: input.targetSkillId } : {}),
        },
        { headers: { 'Idempotency-Key': keyFor(input) } },
      )
    },
    onSuccess: () => {
      if (!tenantId || !space) return
      void queryClient.invalidateQueries({
        queryKey: [`/api/v1/tenants/${tenantId}/spaces/${space.id}/skills`],
      })
    },
  })
}

/**
 * Soft-deletes (archives) a Skill. The Idempotency-Key keeps a retry replaying the
 * same delete instead of erroring on a version change; both the detail and the list
 * query invalidate on success.
 */
export function useDeleteSkill() {
  const queryClient = useQueryClient()
  const { tenantId, space } = useCurrentSpace()
  const keyFor = useIdempotencyKeys()
  return useMutation<Skill, ErrorType<ApiError>, { id: string; version: number }>({
    mutationFn: (input) => {
      if (!tenantId || !space) throw new Error('cloud space not resolved')
      return deleteApiV1TenantsTidSpacesSpaceIdSkillsSkillId(
        tenantId,
        space.id,
        input.id,
        { version: input.version },
        { headers: mutationHeaders(keyFor(input)) },
      )
    },
    onSuccess: (_data, input) => {
      if (!tenantId || !space) return
      void queryClient.invalidateQueries({
        queryKey: [`${skillsKey(tenantId, space.id)}/${input.id}`],
      })
      void queryClient.invalidateQueries({ queryKey: [skillsKey(tenantId, space.id)] })
    },
  })
}
