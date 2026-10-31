import { defineInfiniteQueryOptions, defineMutation, defineQueryOptions, useMutation, useQueryCache } from '@pinia/colada'
import { client, unwrap } from '../../api/client'
import type { CreateKeyBody, CreateUserBody, Summary, UpdateUserBody } from '../../types'
import { toast } from '../toast'

export const usersInfiniteQuery = defineInfiniteQueryOptions({
  key: ['users'],
  initialPageParam: '',
  query: ({ pageParam, signal }) =>
    unwrap(
      client.GET('/admin/v1/users', {
        params: { query: { limit: 100, ...(pageParam ? { after: pageParam } : {}) } },
        signal,
      }),
    ),
  getNextPageParam: (page) => page.next_cursor || undefined,
})

export const userQuery = defineQueryOptions((id: string) => ({
  key: ['users', id],
  enabled: Boolean(id),
  query: () =>
    unwrap(client.GET('/admin/v1/users/{user_id}', {
      params: { path: { user_id: id } },
    })),
}))

export const usageQuery = defineQueryOptions((id: string) => ({
  key: ['users', id, 'usage'],
  enabled: Boolean(id),
  query: async (): Promise<Summary> => {
    const data = await unwrap(
      client.GET('/admin/v1/users/{user_id}/usage', {
        params: { path: { user_id: id } },
      }),
    )
    return { ...data, current_period: data.current_period ?? null }
  },
}))

export const keysQuery = defineQueryOptions((id: string) => ({
  key: ['users', id, 'keys'],
  enabled: Boolean(id),
  query: () =>
    unwrap(
      client.GET('/admin/v1/users/{user_id}/keys', {
        params: { path: { user_id: id }, query: { limit: 100 } },
      }),
    ),
}))

export const policiesQuery = defineQueryOptions((id: string) => ({
  key: ['users', id, 'policies'],
  enabled: Boolean(id),
  query: () =>
    unwrap(
      client.GET('/admin/v1/users/{user_id}/budget-policies', {
        params: { path: { user_id: id }, query: { limit: 100 } },
      }),
    ),
}))

export const useCreateUser = defineMutation(() => {
  const queryCache = useQueryCache()
  return useMutation({
    mutation: (body: CreateUserBody) => unwrap(client.POST('/admin/v1/users', { body })),
    async onSuccess(user) {
      toast('User created', user.id)
      await Promise.all([
        queryCache.invalidateQueries({ key: ['users'], exact: true }),
        queryCache.invalidateQueries({ key: ['overview'] }),
      ])
    },
  })
})

export const useUpdateUser = defineMutation(() => {
  const queryCache = useQueryCache()
  return useMutation({
    mutation: ({ id, body }: { id: string; body: UpdateUserBody }) =>
      unwrap(
        client.PATCH('/admin/v1/users/{user_id}', {
          params: { path: { user_id: id } },
          body,
        }),
      ),
    async onSuccess(user) {
      toast('User updated', user.id)
      await Promise.all([
        queryCache.invalidateQueries({ key: ['users', user.id], exact: true }),
        queryCache.invalidateQueries({ key: ['users', user.id, 'usage'] }),
        queryCache.invalidateQueries({ key: ['users'], exact: true }),
        queryCache.invalidateQueries({ key: ['overview'] }),
      ])
    },
  })
})

export const useCreateKey = defineMutation(() => {
  const queryCache = useQueryCache()
  return useMutation({
    mutation: ({ id, body }: { id: string; body: CreateKeyBody }) =>
      unwrap(
        client.POST('/admin/v1/users/{user_id}/keys', {
          params: { path: { user_id: id } },
          body,
        }),
      ),
    async onSuccess(_result, { id }) {
      await Promise.all([
        queryCache.invalidateQueries({ key: ['users', id, 'keys'] }),
        queryCache.invalidateQueries({ key: ['overview'] }),
      ])
    },
  })
})

export const useRotateKey = defineMutation(() => {
  const queryCache = useQueryCache()
  return useMutation({
    mutation: ({ id, alias }: { id: string; alias: string }) =>
      unwrap(
        client.POST('/admin/v1/users/{user_id}/keys/{key_alias}/rotate', {
          params: { path: { user_id: id, key_alias: alias } },
          body: {},
        }),
      ),
    async onSuccess(_result, { id }) {
      await queryCache.invalidateQueries({ key: ['users', id, 'keys'] })
    },
  })
})

export const useRevokeKey = defineMutation(() => {
  const queryCache = useQueryCache()
  return useMutation({
    mutation: ({ id, alias }: { id: string; alias: string }) =>
      unwrap(
        client.DELETE('/admin/v1/users/{user_id}/keys/{key_alias}', {
          params: { path: { user_id: id, key_alias: alias } },
        }),
      ),
    async onSuccess(_result, { id, alias }) {
      toast('Key revoked', alias)
      await Promise.all([
        queryCache.invalidateQueries({ key: ['users', id, 'keys'] }),
        queryCache.invalidateQueries({ key: ['overview'] }),
      ])
    },
  })
})
