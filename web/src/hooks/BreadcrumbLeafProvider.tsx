import { useMemo, useState, type ReactNode } from "react";
import { BreadcrumbLeafContext } from "./breadcrumbLeafContext";

export function BreadcrumbLeafProvider({ children }: { children: ReactNode }) {
  const [leaf, setLeaf] = useState<string | null>(null);
  const value = useMemo(() => ({ leaf, setLeaf }), [leaf]);
  return (
    <BreadcrumbLeafContext.Provider value={value}>{children}</BreadcrumbLeafContext.Provider>
  );
}
