import { defineInfiniteQueryOptions, defineMutation, defineQueryOptions, useMutation, useQueryCache } from '@pinia/colada'
import { client, unwrap } from '../../api/client'
import type { RequestFilters, ResolveBody } from '../../types'
import { toast } from '../toast'

export const requestsInfiniteQuery = defineInfiniteQueryOptions((filters: RequestFilters) => ({
  key: ['requests', filters],
  initialPageParam: '',
  query: ({ pageParam, signal }) =>
    unwrap(
      client.GET('/admin/v1/requests', {
        params: {
          query: {
            ...filters,
            limit: 100,
            ...(pageParam ? { after: pageParam } : {}),
          },
        },
        signal,
      }),
    ),
  getNextPageParam: (page) => page.next_cursor || undefined,
}))

export const requestQuery = defineQueryOptions((id: string) => ({
  key: ['requests', id],
  enabled: Boolean(id),
  query: () =>
    unwrap(client.GET('/admin/v1/requests/{request}', {
      params: { path: { request: id } },
    })),
}))

export const ledgerQuery = defineQueryOptions((id: string) => ({
  key: ['requests', id, 'ledger'],
  enabled: Boolean(id),
  query: () =>
    unwrap(
      client.GET('/admin/v1/requests/{request}/ledger', {
        params: { path: { request: id }, query: { limit: 100 } },
      }),
    ),
}))

export const useResolveRequest = defineMutation(() => {
  const queryCache = useQueryCache()
  return useMutation({
    mutation: ({ id, body }: { id: string; body: ResolveBody }) =>
      unwrap(
        client.POST('/admin/v1/requests/{request}/resolve', {
          params: { path: { request: id } },
          body,
        }),
      ),
    async onSuccess(request) {
      toast('Request cost resolved', request.id)
      await Promise.all([
        queryCache.invalidateQueries({ key: ['requests', request.id] }),
        queryCache.invalidateQueries({ key: ['requests', request.id, 'ledger'] }),
        queryCache.invalidateQueries({ key: ['requests'] }),
        queryCache.invalidateQueries({ key: ['overview'] }),
      ])
    },
  })
})
