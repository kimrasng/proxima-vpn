import { useCallback, useEffect, useRef, useState } from "react";

// The panel's API answers in tens of milliseconds locally, so a spinner tied
// directly to the request lifetime renders for a single frame and reads as
// "the button did nothing". Holding the busy state for a floor makes the
// refresh legible without making it feel sluggish.
const MIN_SPINNER_MS = 450;

interface ManualRefresh {
  /** True while a click-initiated refresh is in flight (or held for the floor). */
  refreshing: boolean;
  /** When the last successful fetch landed, for the "Last updated" line. */
  lastUpdated: Date | null;
  /** Runs the fetch with the spinner held; safe to call from onClick. */
  refresh: () => void;
  /** Marks a fetch this hook did not initiate (auto-poll, initial load). */
  markUpdated: () => void;
}

/**
 * Drives a header refresh button: a visible busy state, a timestamp of the
 * last successful load, and the interval that keeps the page current.
 *
 * `fetcher` must be stable (useCallback) - it is the interval's dependency.
 * It is expected to handle its own errors; a rejection only clears the
 * spinner and leaves the timestamp untouched, so the page keeps showing when
 * it was last actually current rather than when it last tried.
 */
export function useManualRefresh(fetcher: () => Promise<void>, intervalMs: number): ManualRefresh {
  const [refreshing, setRefreshing] = useState(false);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  // Guards setState after the component is gone: the spinner floor outlives a
  // fast request, so a refresh clicked just before navigating still resolves.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const markUpdated = useCallback(() => {
    if (mounted.current) setLastUpdated(new Date());
  }, []);

  const refresh = useCallback(() => {
    setRefreshing(true);
    void (async () => {
      const started = Date.now();
      try {
        await fetcher();
        markUpdated();
      } finally {
        const held = Date.now() - started;
        if (held < MIN_SPINNER_MS) {
          await new Promise((resolve) => setTimeout(resolve, MIN_SPINNER_MS - held));
        }
        if (mounted.current) setRefreshing(false);
      }
    })();
  }, [fetcher, markUpdated]);

  // The initial load and the poll both go through the plain fetcher: neither is
  // a user action, so neither should light up the button.
  useEffect(() => {
    void fetcher().then(markUpdated);
    const interval = setInterval(() => void fetcher().then(markUpdated), intervalMs);
    return () => clearInterval(interval);
  }, [fetcher, intervalMs, markUpdated]);

  return { refreshing, lastUpdated, refresh, markUpdated };
}
