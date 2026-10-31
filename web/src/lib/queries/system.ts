import { defineMutation, defineQueryOptions, useMutation } from '@pinia/colada'
import { client, messageOf, unwrap } from '../../api/client'
import { toast } from '../toast'

export const configQuery = defineQueryOptions({
  key: ['config'],
  query: () => unwrap(client.GET('/admin/v1/config')),
  staleTime: 5 * 60_000,
})

export const readyQuery = defineQueryOptions({
  key: ['ready'],
  query: () => unwrap(client.GET('/health/ready')),
  staleTime: 0,
})

export const overviewQuery = defineQueryOptions({
  key: ['overview'],
  query: () => unwrap(client.GET('/admin/v1/overview')),
})

export const recentRequestsQuery = defineQueryOptions({
  key: ['requests', 'recent'],
  query: () => unwrap(client.GET('/admin/v1/requests', { params: { query: { limit: 6 } } })),
})

export const recentAuditQuery = defineQueryOptions({
  key: ['audit', 'recent'],
  query: () => unwrap(client.GET('/admin/v1/audit', { params: { query: { limit: 6, order: 'desc' } } })),
})

export const useCreateBackup = defineMutation(() => {
  return useMutation({
    mutation: () => unwrap(client.POST('/admin/v1/backups')),
    onSuccess(result) {
      toast('Backup created', result.filename)
    },
    onError(error) {
      toast('Backup failed', messageOf(error, 'Unable to create a backup.'), 'error')
    },
  })
})
