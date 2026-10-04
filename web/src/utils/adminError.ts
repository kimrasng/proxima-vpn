import { ApiError } from "../api/client";

export function adminError(error: unknown, fallback: string): string {
  if (error instanceof ApiError && error.body !== null && typeof error.body === "object" &&
      "error" in error.body && typeof error.body.error === "string") {
    return error.body.error;
  }
  return fallback;
}
