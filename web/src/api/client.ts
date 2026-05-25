const BASE_URL = import.meta.env.VITE_API_URL || '';

const ADMIN_TOKEN_KEY = 'proxima_admin_token';
const USER_TOKEN_KEY = 'proxima_user_token';

export function getAdminToken(): string | null {
  return localStorage.getItem(ADMIN_TOKEN_KEY);
}

export function setAdminToken(token: string): void {
  localStorage.setItem(ADMIN_TOKEN_KEY, token);
}

export function removeAdminToken(): void {
  localStorage.removeItem(ADMIN_TOKEN_KEY);
}

export function getUserToken(): string | null {
  return localStorage.getItem(USER_TOKEN_KEY);
}

export function setUserToken(token: string): void {
  localStorage.setItem(USER_TOKEN_KEY, token);
}

export function removeUserToken(): void {
  localStorage.removeItem(USER_TOKEN_KEY);
}

type TokenType = 'admin' | 'user';

function getToken(type: TokenType): string | null {
  return type === 'admin' ? getAdminToken() : getUserToken();
}

function removeToken(type: TokenType): void {
  if (type === 'admin') {
    removeAdminToken();
  } else {
    removeUserToken();
  }
}

class ApiError extends Error {
  constructor(
    public status: number,
    public statusText: string,
    public body?: unknown,
  ) {
    super(`API Error ${status}: ${statusText}`);
    this.name = 'ApiError';
  }
}

export { ApiError };

let currentTokenType: TokenType = 'user';

export function setClientTokenType(type: TokenType): void {
  currentTokenType = type;
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const token = getToken(currentTokenType);

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  };

  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const config: RequestInit = {
    method,
    headers,
  };

  if (body !== undefined) {
    config.body = JSON.stringify(body);
  }

  const response = await fetch(`${BASE_URL}${path}`, config);

  if (response.status === 401) {
    const isLoginEndpoint = path.includes('/auth/login');
    if (!isLoginEndpoint) {
      removeToken(currentTokenType);
      window.location.href = currentTokenType === 'admin' ? '/admin/login' : '/login';
      throw new ApiError(401, 'Unauthorized');
    }
  }

  if (!response.ok) {
    let errorBody: unknown;
    try {
      errorBody = await response.json();
    } catch {
      errorBody = undefined;
    }
    throw new ApiError(response.status, response.statusText, errorBody);
  }

  if (response.status === 204) {
    return undefined as T;
  }

  return response.json() as Promise<T>;
}

export function get<T>(path: string): Promise<T> {
  return request<T>('GET', path);
}

export function post<T>(path: string, body?: unknown): Promise<T> {
  return request<T>('POST', path, body);
}

export function put<T>(path: string, body?: unknown): Promise<T> {
  return request<T>('PUT', path, body);
}

export function del<T>(path: string): Promise<T> {
  return request<T>('DELETE', path);
}
