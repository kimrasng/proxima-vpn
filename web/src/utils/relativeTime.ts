import type { TFunction } from "i18next";

/**
 * Formats an absolute timestamp as translated "N units ago".
 *
 * Takes `t` rather than calling useTranslation itself so it stays usable inside
 * table `cell` callbacks, which are plain functions and cannot call hooks.
 */
export function formatRelativeTime(t: TFunction, dateStr: string | undefined | null): string {
  if (!dateStr) return t("common.relativeTime.never");

  const diffMs = Date.now() - new Date(dateStr).getTime();
  // A clock skew between the node and the panel can put the stamp slightly in
  // the future; reporting that as a negative age would read as nonsense.
  if (diffMs < 60_000) return t("common.relativeTime.justNow");

  const minutes = Math.floor(diffMs / 60_000);
  if (minutes < 60) return t("common.relativeTime.minutesAgo", { count: minutes });

  const hours = Math.floor(minutes / 60);
  if (hours < 24) return t("common.relativeTime.hoursAgo", { count: hours });

  return t("common.relativeTime.daysAgo", { count: Math.floor(hours / 24) });
}

/** Absolute stamp for the tooltip behind a relative one. */
export function formatAbsoluteTime(dateStr: string | undefined | null): string {
  return dateStr ? new Date(dateStr).toLocaleString() : "";
}
