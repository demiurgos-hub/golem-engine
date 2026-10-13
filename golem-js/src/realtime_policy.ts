export function isAllowedFallbackPair(primary: unknown, fallback: unknown): boolean {
  return primary === "webtransport" && fallback === "websocket";
}

export function classifyAckInterval(value: unknown): "not-integer" | "out-of-range" | undefined {
  if (typeof value !== "number" || !Number.isFinite(value) || !Number.isInteger(value)) return "not-integer";
  if (value < 0 || value > 0x7fffffff) return "out-of-range";
  return undefined;
}
