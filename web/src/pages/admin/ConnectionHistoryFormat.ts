// Never pass history bytes through Number: a single counter may exceed 2^53.
export function formatHistoryBytes(value: string | null, unknown: string): string {
  return value === null ? unknown : `${BigInt(value).toLocaleString()} B`;
}

export function historyLocalTime(value: string | null, unknown: string): string {
  return value === null ? unknown : new Date(value).toLocaleString(undefined, { timeZoneName: 'short' });
}
