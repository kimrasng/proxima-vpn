import type { LocalizedText } from "../api/types";
import { resolveLanguage } from "./language";

export function pickLocalized(text: LocalizedText | undefined, language: string | undefined): string {
  if (!text) return "";
  return text[resolveLanguage(language)] || text.en || text.ko || text.zh || "";
}
