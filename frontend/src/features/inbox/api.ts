import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { mockApi } from '@/lib/mock-api-client'
import type { InboxItem } from '@/mocks/data/types'

export function useInboxItems(slug: string, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: ['inbox', slug],
    queryFn: async () => {
      const { data } = await mockApi.get<InboxItem[]>(`/workspaces/${slug}/inbox`)
      return data
    },
    enabled,
  })
}

export function useMarkInboxRead(slug: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, read }: { id: string; read: boolean }) => {
      const { data } = await mockApi.patch<InboxItem>(`/workspaces/${slug}/inbox/${id}`, { read })
      return data
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['inbox', slug] }),
  })
}

export function useMarkAllInboxRead(slug: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      await mockApi.post(`/workspaces/${slug}/inbox/mark-all-read`)
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['inbox', slug] }),
  })
}
