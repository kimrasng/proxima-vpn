export type UiLanguage = "ko" | "en" | "zh";

/** Resolves the detector's value (e.g. "ko-KR") to one of the supported UI languages. */
export function resolveLanguage(language: string | undefined): UiLanguage {
  const base = (language ?? "").toLowerCase().split("-")[0];
  return base === "en" || base === "zh" ? base : "ko";
}
