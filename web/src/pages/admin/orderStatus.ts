import type { StatusIndicatorProps } from "@cloudscape-design/components";
import type { OrderStatus } from "../../api/types";

export function orderStatusIndicatorType(status: OrderStatus): StatusIndicatorProps.Type {
  switch (status) {
    case "paid":
      return "success";
    case "cancelled":
      return "stopped";
    // Matches the user-facing table and the account status columns: a lapsed
    // window needs attention but nothing failed.
    case "expired":
      return "warning";
    case "pending":
      return "pending";
  }
}

// `outcome` is read as an open string rather than the schema's CHECK list: an
// outcome added server-side must still render, so unknown values fall through to
// a neutral marker instead of being dropped.
export function paymentOutcomeIndicatorType(outcome: string): StatusIndicatorProps.Type {
  switch (outcome) {
    case "granted":
      return "success";
    case "failed":
      return "error";
    case "received":
      return "pending";
    case "duplicate":
    case "ignored":
      return "info";
    default:
      return "info";
  }
}
