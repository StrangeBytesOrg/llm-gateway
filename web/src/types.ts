import type { components, paths } from './api/schema'

type Schemas = components['schemas']

export type User = Schemas['User']
export type PeriodUsage = Schemas['PeriodUsage']
export type Key = Schemas['Key']
export type CreateKeyResult = Schemas['CreateKeyResult']
export type Request = Schemas['Request']
export type LedgerEntry = Schemas['LedgerEntry']
export type AuditEntry = Schemas['AuditEntry']
export type BudgetPolicy = Schemas['BudgetPolicy']
export type Overview = Schemas['Overview']
export type AdminConfig = Schemas['AdminConfig']
export type SessionInfo = Schemas['SessionInfo']
export type CreateSessionResult = Schemas['CreateSessionResult']
export type BackupResult = Schemas['BackupResult']
export type StatusResult = Schemas['StatusResult']

// The Go summary may return a null current period; the generated schema does
// not encode that one object-pointer nullability.
export type Summary = Omit<Schemas['Summary'], 'current_period'> & {
  current_period: PeriodUsage | null
}

export type CreateUserBody = Schemas['CreateUserInputBody']
export type UpdateUserBody = Schemas['UpdateUserInputBody']
export type CreateKeyBody = Schemas['CreateKeyInputBody']
export type RotateKeyBody = Schemas['RotateKeyBody']
export type ResolveBody = Schemas['ResolveInputBody']

export type RequestsQuery = NonNullable<paths['/admin/v1/requests']['get']['parameters']['query']>
export type RequestFilters = Pick<RequestsQuery, 'user_id' | 'accounting'>
