import { useCallback, useEffect, useState } from "react";

// Each dashboard section loads independently so an unavailable auxiliary API
// cannot turn an account overview into a misleading empty-plan state.
export function useUserResource<T>(load: () => Promise<T>, refreshKey = 0) {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [revision, setRevision] = useState(0);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(false);
    void load().then(
      (result) => {
        if (active) setData(result);
      },
      () => {
        if (active) {
          setData(null);
          setError(true);
        }
      },
    ).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [load, revision, refreshKey]);

  return { data, setData, loading, error, refresh };
}
