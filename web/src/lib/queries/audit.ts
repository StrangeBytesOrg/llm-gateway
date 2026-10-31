import { defineInfiniteQueryOptions } from '@pinia/colada'
import { client, unwrap } from '../../api/client'

export const auditInfiniteQuery = defineInfiniteQueryOptions({
  key: ['audit'],
  initialPageParam: '',
  query: ({ pageParam, signal }) =>
    unwrap(
      client.GET('/admin/v1/audit', {
        params: {
          query: {
            limit: 100,
            order: 'desc',
            ...(pageParam ? { after: pageParam } : {}),
          },
        },
        signal,
      }),
    ),
  getNextPageParam: (page) => page.next_cursor || undefined,
})
