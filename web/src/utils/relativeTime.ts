import type { TFunction } from "i18next";
import i18n from "../i18n";

// Date formatting must follow the language the admin picked, not the browser's.
// A bare toLocaleString() renders "9/17/2026, 1:52:43 PM" inside an otherwise
// Korean page whenever the two disagree.
function activeLocale(): string {
  return i18n.language || "en";
}

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
  return dateStr ? new Date(dateStr).toLocaleString(activeLocale()) : "";
}

/** Date-only stamp, for columns where the time of day is noise. */
export function formatDate(dateStr: string | undefined | null): string {
  return dateStr ? new Date(dateStr).toLocaleDateString(activeLocale()) : "";
}

/** Clock-only stamp, for a "last refreshed" line. */
export function formatTime(date: Date): string {
  return date.toLocaleTimeString(activeLocale());
}

/**
 * Formats an elapsed number of seconds as a compact duration, coarsening as it
 * grows so a long outage reads as "3d 4h" rather than a five-figure minute count.
 */
export function formatDuration(t: TFunction, seconds: number): string {
  if (seconds < 60) return t("common.duration.seconds", { count: Math.max(1, Math.round(seconds)) });

  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return t("common.duration.minutes", { count: minutes });

  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    const restMinutes = minutes % 60;
    return restMinutes > 0
      ? `${t("common.duration.hours", { count: hours })} ${t("common.duration.minutes", { count: restMinutes })}`
      : t("common.duration.hours", { count: hours });
  }

  const days = Math.floor(hours / 24);
  const restHours = hours % 24;
  return restHours > 0
    ? `${t("common.duration.days", { count: days })} ${t("common.duration.hours", { count: restHours })}`
    : t("common.duration.days", { count: days });
}
