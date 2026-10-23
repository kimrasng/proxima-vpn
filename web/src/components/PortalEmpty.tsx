import type { ReactNode } from "react";
import { Box, Container, SpaceBetween } from "@cloudscape-design/components";

// One empty state for every portal page, so a page with nothing to show still
// reads as a deliberate section rather than a bare header or floating text.
export default function PortalEmpty({
  title, description, action,
}: { title: string; description?: string; action?: ReactNode }) {
  return (
    <Container>
      <Box textAlign="center" padding={{ vertical: "l" }}>
        <SpaceBetween size="xs" alignItems="center">
          <Box variant="h3" padding="n">{title}</Box>
          {description && <Box color="text-body-secondary">{description}</Box>}
          {action && <Box margin={{ top: "s" }}>{action}</Box>}
        </SpaceBetween>
      </Box>
    </Container>
  );
}
