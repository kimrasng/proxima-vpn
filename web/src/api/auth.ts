import { get, post, setClientTokenType } from './client';
import {
  getAdminToken,
  getUserToken,
  setAdminToken,
  setUserToken,
  removeAdminToken,
  removeUserToken,
} from './client';
import type { LoginRequest, LoginResponse, RegisterRequest, RegisterResponse } from './types';

export function adminLogin(req: LoginRequest): Promise<LoginResponse> {
  setClientTokenType('admin');
  return post<LoginResponse>('/api/v1/admin/auth/login', req);
}

export function userLogin(req: LoginRequest): Promise<LoginResponse> {
  return post<LoginResponse>('/api/v1/auth/login', req);
}

export function userRegister(req: RegisterRequest): Promise<RegisterResponse> {
  return post<RegisterResponse>('/api/v1/auth/register', req);
}

export interface SignupPolicy {
  restricted: boolean;
  allowed_domains: string[];
}

// Public: lets the sign-up page show the allowed email domains before anyone types.
export function getSignupPolicy(): Promise<SignupPolicy> {
  return get<SignupPolicy>('/api/v1/auth/signup-policy');
}

export function getToken(type: 'admin' | 'user'): string | null {
  return type === 'admin' ? getAdminToken() : getUserToken();
}

export function setToken(type: 'admin' | 'user', token: string): void {
  if (type === 'admin') {
    setAdminToken(token);
  } else {
    setUserToken(token);
  }
}

export function removeToken(type: 'admin' | 'user'): void {
  if (type === 'admin') {
    removeAdminToken();
  } else {
    removeUserToken();
  }
}

export function isAuthenticated(type: 'admin' | 'user'): boolean {
  return getToken(type) !== null;
}
