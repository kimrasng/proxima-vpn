import { del, get, post, setClientTokenType } from './client';
import type { AuthenticationResponseJSON, PublicKeyCredentialCreationOptionsJSON, PublicKeyCredentialRequestOptionsJSON, RegistrationResponseJSON } from '@simplewebauthn/browser';

export interface Passkey {
  id: string;
  name: string;
  created_at: string;
  last_used_at: string | null;
}

function applyUserClient() { setClientTokenType('user'); }

export interface PasskeyLoginBegin { session_id: string; options: PublicKeyCredentialRequestOptionsJSON }
export interface PasskeyRegistrationBegin { options: PublicKeyCredentialCreationOptionsJSON }

export async function beginPasskeyLogin(): Promise<PasskeyLoginBegin> {
  return post<PasskeyLoginBegin>('/api/v1/auth/passkey/login/begin');
}
export async function finishPasskeyLogin(session_id: string, credential: AuthenticationResponseJSON): Promise<{ token: string }> {
  return post('/api/v1/auth/passkey/login/finish', { session_id, credential });
}
export async function getPasskeys(): Promise<Passkey[]> {
  applyUserClient(); return get('/api/v1/user/passkeys');
}
export async function beginPasskeyRegistration(password: string): Promise<PasskeyRegistrationBegin> {
  applyUserClient(); return post('/api/v1/user/passkeys/register/begin', { password });
}
export async function finishPasskeyRegistration(name: string, credential: RegistrationResponseJSON): Promise<Passkey> {
  applyUserClient(); return post('/api/v1/user/passkeys/register/finish', { name, credential });
}
export async function deletePasskey(id: string): Promise<void> {
  applyUserClient(); return del(`/api/v1/user/passkeys/${encodeURIComponent(id)}`);
}
