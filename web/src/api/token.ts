const tokenKey = 'gateway.admin.session'

export function getToken(): string | null {
  return sessionStorage.getItem(tokenKey)
}

export function setToken(token: string): void {
  sessionStorage.setItem(tokenKey, token)
}

export function clearToken(): void {
  sessionStorage.removeItem(tokenKey)
}
