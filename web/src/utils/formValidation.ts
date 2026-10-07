// Forms follow the Cloudscape validation pattern: nothing is flagged until the
// first save attempt, after which each field re-validates as it is edited and
// its message sits under the control it belongs to (FormField `errorText`)
// rather than in a page-level banner.

/**
 * Moves focus to the first visible control that a FormField marked invalid.
 *
 * Deferred to the next frame because `aria-invalid` only appears once React
 * has rendered the errorText that sets it. Hidden matches are skipped: closed
 * Cloudscape modals keep their content mounted but undisplayed, and focusing
 * one of those would silently do nothing.
 */
export function focusFirstInvalid(): void {
  requestAnimationFrame(() => {
    const target = Array.from(
      document.querySelectorAll<HTMLElement>('[aria-invalid="true"]'),
    ).find((element) => element.getClientRects().length > 0);
    target?.focus();
  });
}

export function hasFieldErrors(errors: Record<string, string | undefined>): boolean {
  return Object.values(errors).some(Boolean);
}
