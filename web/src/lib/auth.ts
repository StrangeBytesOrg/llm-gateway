import { defineMutation, defineQueryOptions, useMutation, useQueryCache } from '@pinia/colada'
import { client, unwrap } from '../api/client'
import { clearToken, setToken } from '../api/token'

export const sessionQuery = defineQueryOptions({
  key: ['session'],
  query: () => unwrap(client.GET('/admin/v1/session')),
})

export const useLogin = defineMutation(() => {
  const queryCache = useQueryCache()
  return useMutation({
    mutation: ({ username, password }: { username: string; password: string }) =>
      unwrap(client.POST('/admin/v1/session', { body: { username, password } }), {
        allowUnauthorized: true,
      }),
    async onSuccess(session) {
      setToken(session.token)
      await queryCache.invalidateQueries({ key: ['session'] })
    },
  })
})

export const useLogout = defineMutation(() => {
  const queryCache = useQueryCache()
  return useMutation({
    mutation: () => unwrap(client.DELETE('/admin/v1/session')),
    onSettled() {
      clearToken()
      queryCache.cancelQueries()
      for (const entry of queryCache.getEntries()) queryCache.remove(entry)
    },
  })
})
