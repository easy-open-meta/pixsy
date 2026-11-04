import { request } from './client'

export interface User {
  id: number
  username: string
  role: string
  created_at: string
}

export interface Credentials {
  username: string
  password: string
}

export interface LoginResult {
  token: string
  user: User
}

export function login(creds: Credentials): Promise<LoginResult> {
  return request<LoginResult>('POST', '/auth/login', creds)
}

/** 注册只返回用户，登录态需再调一次 login（§3.3）。 */
export function register(creds: Credentials): Promise<{ user: User }> {
  return request<{ user: User }>('POST', '/auth/register', creds)
}

export function me(): Promise<{ user: User }> {
  return request<{ user: User }>('GET', '/auth/me')
}
