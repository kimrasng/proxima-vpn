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

export interface SignupCodeResponse { message: string; expires_in_seconds: number; signup_token: string }
export function requestSignupEmailCode(req: RegisterRequest & { language?: string; signup_token?: string }): Promise<SignupCodeResponse> {
  return post('/api/v1/auth/register/request-code', req);
}
export function verifySignupEmailCode(req: RegisterRequest & { code: string; signup_token: string; language?: string }): Promise<RegisterResponse> {
  return post('/api/v1/auth/register/verify-code', req);
}

export type ResetLanguage = 'ko' | 'en' | 'zh';

export interface MessageResponse {
  message: string;
}

// The server answers 200 whether or not the address belongs to an account, so
// callers must not infer account existence from the result.
export function forgotPassword(req: { email: string; language: ResetLanguage }): Promise<MessageResponse> {
  return post<MessageResponse>('/api/v1/auth/forgot-password', req);
}

export function resetPassword(req: { token: string; password: string }): Promise<MessageResponse> {
  return post<MessageResponse>('/api/v1/auth/reset-password', req);
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
