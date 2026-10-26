// Where to land after a portal sign-in that interrupted another page (the
// OAuth consent screen). The target travels in the login URL as
// /login?continue=<path>, with a sessionStorage copy as a fallback for the
// shared 401 handler, which only knows to send the user to /login. Only
// same-origin absolute paths are accepted, so a crafted value can never send a
// freshly signed-in user to another site.
import { pendingPromo, promoDashboardTarget } from './pendingPromo';

const KEY = 'proxima_post_login';

export function isSafePath(path: string): boolean {
  if (!path.startsWith('/') || path.startsWith('//') || path.startsWith('/\\')) return false;
  // Control characters would let "/\tevil.example" parse as an absolute URL.
  // eslint-disable-next-line no-control-regex -- deliberate: rejecting control chars is the point
  return !/[\x00-\x20]/.test(path);
}

export function rememberPostLogin(path: string): void {
  const stored = sessionStorage.getItem(KEY);
  if (stored?.startsWith('/oauth/authorize') && path.startsWith('/portal/dashboard?promo=')) return;
  if (isSafePath(path)) sessionStorage.setItem(KEY, path);
}

export function loginUrl(path: string): string {
  if (!isSafePath(path) || path === '/login' || path.startsWith('/login?')) return '/login';
  return `/login?continue=${encodeURIComponent(path)}`;
}

export function continueTarget(search: string): string | null {
  const params = new URLSearchParams(search);
  const value = params.get('continue');
  if (value && isSafePath(value)) return value;
  const promo = params.get('promo')?.trim();
  const durationDays = Number(params.get('duration_days')) || undefined;
  return promo ? promoDashboardTarget(promo, durationDays) : null;
}

export function consumePostLogin(fallback: string, search = window.location.search): string {
  const fromUrl = continueTarget(search);
  const stored = sessionStorage.getItem(KEY);
  sessionStorage.removeItem(KEY);
  const explicitContinue = new URLSearchParams(search).get('continue');
  if (explicitContinue && isSafePath(explicitContinue)) return explicitContinue;
  if (stored && isSafePath(stored)) return stored;
  if (fromUrl) return fromUrl;
  const promo = pendingPromo();
  return promo ? promoDashboardTarget(promo) : fallback;
}
