import { useContext, useEffect } from "react";
import { BreadcrumbLeafContext } from "./breadcrumbLeafContext";

export function useBreadcrumbLeafValue(): string | null {
  return useContext(BreadcrumbLeafContext)?.leaf ?? null;
}

// Detail pages publish the label of the record they are showing, which the
// layout cannot know: the route only carries an opaque id.
export function usePublishBreadcrumbLeaf(label: string | null | undefined) {
  const context = useContext(BreadcrumbLeafContext);
  const setLeaf = context?.setLeaf;

  useEffect(() => {
    if (!setLeaf) return;
    setLeaf(label ?? null);
    return () => setLeaf(null);
  }, [setLeaf, label]);
}
