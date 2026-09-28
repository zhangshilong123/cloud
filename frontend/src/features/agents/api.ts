import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type {
  Agent,
  AgentSkillBinding,
  AgentSkillBindingListItem,
  AgentStatus,
  Error as ApiError,
  ExecutionRecord,
} from '@/api/generated.schemas'
import {
  deleteApiV1TenantsTidSpacesSpaceIdAgentsAgentId,
  deleteApiV1TenantsTidSpacesSpaceIdAgentsAgentIdSkillsSkillId,
  getApiV1TenantsTidSpacesSpaceIdAgents,
  getApiV1TenantsTidSpacesSpaceIdAgentsAgentId,
  getApiV1TenantsTidSpacesSpaceIdAgentsAgentIdSkills,
  patchApiV1TenantsTidSpacesSpaceIdAgentsAgentId,
  postApiV1TenantsTidSpacesSpaceIdAgents,
  postApiV1TenantsTidSpacesSpaceIdAgentsAgentIdSkills,
  putApiV1TenantsTidSpacesSpaceIdAgentsAgentIdSkillsSkillId,
} from '@/api/agents/agents'
import {
  getApiV1TenantsTidSpacesSpaceIdExecutionsExecutionId,
  postApiV1TenantsTidSpacesSpaceIdExecutions,
} from '@/api/executions/executions'
import { useCurrentSpace } from '@/features/spaces/current-space'
import { mutationHeaders, useIdempotencyKeys } from '@/features/spaces/api'
import { useCursorList } from '@/features/spaces/cursor-list'
import type { ErrorType } from '@/lib/api-client'

function agentsKey(tenantId: string, spaceId: string): string {
  return `/api/v1/tenants/${tenantId}/spaces/${spaceId}/agents`
}

function agentDetailKey(tenantId: string, spaceId: string, agentId: string): string {
  return `/api/v1/tenants/${tenantId}/spaces/${spaceId}/agents/${agentId}`
}

function bindingsKey(tenantId: string, spaceId: string, agentId: string): string {
  return `/api/v1/tenants/${tenantId}/spaces/${spaceId}/agents/${agentId}/skills`
}

function spaceIdFor(
  slug: string,
  space: { slug: string; id: string } | undefined,
): string | undefined {
  return space?.slug === slug ? space.id : undefined
}

/**
 * Live Agents of the current space, cursor-paginated through the shared
 * space-scoped list query. Disabled until the slug resolves to a joined space.
 */
export function useAgents(slug: string) {
  return useCursorList<Agent>(slug, 'agents', agentsKey, (tenantId, spaceId, params, signal) =>
    getApiV1TenantsTidSpacesSpaceIdAgents(tenantId, spaceId, params, undefined, signal),
  )
}

/** One live Agent by id, used by the detail page for name/status/version. */
export function useAgent(slug: string, agentId: string | undefined) {
  const { tenantId, space } = useCurrentSpace()
  const spaceId = spaceIdFor(slug, space)
  return useQuery({
    queryKey:
      tenantId && spaceId && agentId
        ? [agentDetailKey(tenantId, spaceId, agentId)]
        : ['agent', 'disabled'],
    queryFn: ({ signal }) =>
      getApiV1TenantsTidSpacesSpaceIdAgentsAgentId(
        tenantId ?? '',
        spaceId ?? '',
        agentId ?? '',
        undefined,
        signal,
      ),
    enabled: !!tenantId && !!spaceId && !!agentId,
  })
}

/** Creates an Agent (name only) in the current space; the list invalidates on success. */
export function useCreateAgent() {
  const queryClient = useQueryClient()
  const { tenantId, space } = useCurrentSpace()
  const keyFor = useIdempotencyKeys()
  return useMutation<Agent, ErrorType<ApiError>, { name: string }>({
    mutationFn: (input) => {
      if (!tenantId || !space) throw new Error('cloud space not resolved')
      return postApiV1TenantsTidSpacesSpaceIdAgents(
        tenantId,
        space.id,
        { name: input.name },
        { headers: mutationHeaders(keyFor(input)) },
      )
    },
    onSuccess: () => {
      if (!tenantId || !space) return
      void queryClient.invalidateQueries({ queryKey: [agentsKey(tenantId, space.id)] })
    },
  })
}

/** Rename and/or enable-disable an Agent with an optimistic version guard. */
export function useUpdateAgent() {
  const queryClient = useQueryClient()
  const { tenantId, space } = useCurrentSpace()
  return useMutation<
    Agent,
    ErrorType<ApiError>,
    { id: string; version: number; name?: string; status?: AgentStatus }
  >({
    mutationFn: (input) => {
      if (!tenantId || !space) throw new Error('cloud space not resolved')
      return patchApiV1TenantsTidSpacesSpaceIdAgentsAgentId(tenantId, space.id, input.id, {
        version: input.version,
        ...(input.name !== undefined ? { name: input.name } : {}),
        ...(input.status !== undefined ? { status: input.status } : {}),
      })
    },
    onSuccess: (_agent, input) => {
      if (!tenantId || !space) return
      void queryClient.invalidateQueries({ queryKey: [agentsKey(tenantId, space.id)] })
      void queryClient.invalidateQueries({
        queryKey: [agentDetailKey(tenantId, space.id, input.id)],
      })
    },
  })
}

/** Soft-deletes (archives) an Agent; the list invalidates on success. */
export function useArchiveAgent() {
  const queryClient = useQueryClient()
  const { tenantId, space } = useCurrentSpace()
  const keyFor = useIdempotencyKeys()
  return useMutation<Agent, ErrorType<ApiError>, { id: string; version: number }>({
    mutationFn: (input) => {
      if (!tenantId || !space) throw new Error('cloud space not resolved')
      return deleteApiV1TenantsTidSpacesSpaceIdAgentsAgentId(
        tenantId,
        space.id,
        input.id,
        { version: input.version },
        { headers: mutationHeaders(keyFor(input)) },
      )
    },
    onSuccess: () => {
      if (!tenantId || !space) return
      void queryClient.invalidateQueries({ queryKey: [agentsKey(tenantId, space.id)] })
    },
  })
}

/** The Agent's Skill bindings (enabled and disabled), in deterministic order. */
export function useAgentSkillBindings(slug: string, agentId: string | undefined) {
  const { tenantId, space } = useCurrentSpace()
  const spaceId = spaceIdFor(slug, space)
  return useQuery({
    queryKey:
      tenantId && spaceId && agentId
        ? [bindingsKey(tenantId, spaceId, agentId)]
        : ['agent-skills', 'disabled'],
    queryFn: ({ signal }) =>
      getApiV1TenantsTidSpacesSpaceIdAgentsAgentIdSkills(
        tenantId ?? '',
        spaceId ?? '',
        agentId ?? '',
        undefined,
        undefined,
        signal,
      ),
    enabled: !!tenantId && !!spaceId && !!agentId,
  })
}

/** Attaches a Skill (by id) to an Agent; the binding list invalidates on success. */
export function useAttachSkill() {
  const queryClient = useQueryClient()
  const { tenantId, space } = useCurrentSpace()
  const keyFor = useIdempotencyKeys()
  return useMutation<AgentSkillBinding, ErrorType<ApiError>, { agentId: string; skillId: string }>({
    mutationFn: (input) => {
      if (!tenantId || !space) throw new Error('cloud space not resolved')
      return postApiV1TenantsTidSpacesSpaceIdAgentsAgentIdSkills(
        tenantId,
        space.id,
        input.agentId,
        { skillId: input.skillId },
        { headers: mutationHeaders(keyFor(input)) },
      )
    },
    onSuccess: (_binding, input) => {
      if (!tenantId || !space) return
      void queryClient.invalidateQueries({
        queryKey: [bindingsKey(tenantId, space.id, input.agentId)],
      })
    },
  })
}

/** Enable/disable one binding; the `enabled` flag is version-guarded (428/409). */
export function useSetSkillEnabled() {
  const queryClient = useQueryClient()
  const { tenantId, space } = useCurrentSpace()
  return useMutation<
    AgentSkillBinding,
    ErrorType<ApiError>,
    { agentId: string; skillId: string; enabled: boolean; version: number }
  >({
    mutationFn: (input) => {
      if (!tenantId || !space) throw new Error('cloud space not resolved')
      return putApiV1TenantsTidSpacesSpaceIdAgentsAgentIdSkillsSkillId(
        tenantId,
        space.id,
        input.agentId,
        input.skillId,
        { enabled: input.enabled, version: input.version },
      )
    },
    onSuccess: (_binding, input) => {
      if (!tenantId || !space) return
      void queryClient.invalidateQueries({
        queryKey: [bindingsKey(tenantId, space.id, input.agentId)],
      })
    },
  })
}

/** Detaches a Skill binding; the list invalidates on success. */
export function useDetachSkill() {
  const queryClient = useQueryClient()
  const { tenantId, space } = useCurrentSpace()
  const keyFor = useIdempotencyKeys()
  return useMutation<unknown, ErrorType<ApiError>, { agentId: string; skillId: string }>({
    mutationFn: (input) => {
      if (!tenantId || !space) throw new Error('cloud space not resolved')
      return deleteApiV1TenantsTidSpacesSpaceIdAgentsAgentIdSkillsSkillId(
        tenantId,
        space.id,
        input.agentId,
        input.skillId,
        {},
        { headers: mutationHeaders(keyFor(input)) },
      )
    },
    onSuccess: (_result, input) => {
      if (!tenantId || !space) return
      void queryClient.invalidateQueries({
        queryKey: [bindingsKey(tenantId, space.id, input.agentId)],
      })
    },
  })
}

/** Admits one Execution for an Agent; the body carries only the agent identity. */
export function useCreateExecution() {
  const { tenantId, space } = useCurrentSpace()
  const keyFor = useIdempotencyKeys()
  return useMutation<ExecutionRecord, ErrorType<ApiError>, { agentId: string }>({
    mutationFn: (input) => {
      if (!tenantId || !space) throw new Error('cloud space not resolved')
      return postApiV1TenantsTidSpacesSpaceIdExecutions(
        tenantId,
        space.id,
        { agentId: input.agentId },
        { headers: mutationHeaders(keyFor(input)) },
      )
    },
  })
}

/** Reads one frozen Execution record by id, scoped to the current space. */
export function useExecution(slug: string, executionId: string | undefined) {
  const { tenantId, space } = useCurrentSpace()
  const spaceId = spaceIdFor(slug, space)
  return useQuery({
    queryKey:
      tenantId && spaceId && executionId
        ? [`/api/v1/tenants/${tenantId}/spaces/${spaceId}/executions/${executionId}`]
        : ['execution', 'disabled'],
    queryFn: ({ signal }) =>
      getApiV1TenantsTidSpacesSpaceIdExecutionsExecutionId(
        tenantId ?? '',
        spaceId ?? '',
        executionId ?? '',
        undefined,
        signal,
      ),
    enabled: !!tenantId && !!spaceId && !!executionId,
  })
}

export type { Agent, AgentSkillBinding, AgentSkillBindingListItem }
