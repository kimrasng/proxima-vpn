// A /p/{code} visit stores the campaign code so it survives signup, email
// verification and login, which may happen in another tab or days later. The
// dashboard presents it once; checkout only starts after an explicit acceptance.
const KEY = 'proxima_pending_promo';
const MAX_AGE_MS = 30 * 24 * 60 * 60 * 1000;

export function rememberPromo(code: string, durationDays?: number): void {
  const value = code.trim();
  if (value) localStorage.setItem(KEY, JSON.stringify({ code: value, durationDays, savedAt: Date.now() }));
}

export function pendingPromo(): string | null {
  try {
    const saved = JSON.parse(localStorage.getItem(KEY) ?? 'null') as { code?: unknown; savedAt?: unknown } | null;
    if (!saved || typeof saved.code !== 'string' || typeof saved.savedAt !== 'number') return null;
    if (Date.now() - saved.savedAt > MAX_AGE_MS) {
      localStorage.removeItem(KEY);
      return null;
    }
    return saved.code;
  } catch {
    localStorage.removeItem(KEY);
    return null;
  }
}

export function clearPromo(): void {
  localStorage.removeItem(KEY);
}

export function pendingPromoDuration(code: string): number | undefined {
  if (pendingPromo() !== code) return undefined;
  const saved: { durationDays?: unknown } = JSON.parse(localStorage.getItem(KEY) ?? "{}");
  return typeof saved.durationDays === "number" && saved.durationDays > 0 ? saved.durationDays : undefined;
}

export function promoDashboardTarget(code: string, durationDays = pendingPromoDuration(code)): string {
  const params = new URLSearchParams({ promo: code });
  if (durationDays) params.set("duration_days", String(durationDays));
  return `/portal/dashboard?${params}`;
}

export function promoOfferSeen(email: string, code: string): boolean {
  return localStorage.getItem(`proxima_offer_seen:${email.toLowerCase()}:${code}`) !== null;
}

export function markPromoOfferSeen(email: string, code: string): void {
  localStorage.setItem(`proxima_offer_seen:${email.toLowerCase()}:${code}`, "1");
}
