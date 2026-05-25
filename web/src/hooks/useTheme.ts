import { useCallback, useEffect, useSyncExternalStore } from "react";
import { applyMode, Mode } from "@cloudscape-design/global-styles";

const STORAGE_KEY = "proxima_theme";

type Theme = "light" | "dark";

function getSystemPreference(): Theme {
  return window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

function getStoredTheme(): Theme | null {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === "light" || stored === "dark") return stored;
  return null;
}

function resolveTheme(): Theme {
  return getStoredTheme() ?? getSystemPreference();
}

function applyTheme(theme: Theme) {
  applyMode(theme === "dark" ? Mode.Dark : Mode.Light);
}

let currentTheme: Theme = resolveTheme();
const listeners = new Set<() => void>();

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

function getSnapshot(): Theme {
  return currentTheme;
}

function setTheme(theme: Theme) {
  currentTheme = theme;
  localStorage.setItem(STORAGE_KEY, theme);
  applyTheme(theme);
  listeners.forEach((cb) => cb());
}

/** Initialize theme on app startup (call once before render). */
export function initTheme() {
  currentTheme = resolveTheme();
  applyTheme(currentTheme);
}

export function useTheme() {
  const theme = useSyncExternalStore(subscribe, getSnapshot);

  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => {

      if (!getStoredTheme()) {
        setTheme(getSystemPreference());
      }
    };
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, []);

  const toggle = useCallback(() => {
    setTheme(currentTheme === "dark" ? "light" : "dark");
  }, []);

  return { theme, toggle } as const;
}
