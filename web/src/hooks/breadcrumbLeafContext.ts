import { createContext } from "react";

export interface BreadcrumbLeafValue {
  leaf: string | null;
  setLeaf: (label: string | null) => void;
}

export const BreadcrumbLeafContext = createContext<BreadcrumbLeafValue | null>(null);
