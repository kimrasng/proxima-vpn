import type { ReactNode } from "react";
import { Box } from "@cloudscape-design/components";
import { formatAbsoluteTime } from "../../utils/relativeTime";

const EMPTY = "—";

// An empty string from the audit endpoint means the column was captured but
// held nothing, which reads as the same dash as a nullable timestamp: the
// distinction that matters to a reviewer is section-level (see the
// request_context and grant "not captured" notices), not per field.
export function textOrEmpty(value: string): ReactNode {
  return value ? value : <Box color="text-status-inactive">{EMPTY}</Box>;
}

export function timeOrEmpty(value: string | null): ReactNode {
  return value ? formatAbsoluteTime(value) : <Box color="text-status-inactive">{EMPTY}</Box>;
}
