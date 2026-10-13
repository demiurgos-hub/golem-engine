export function redactURL(url: string): string {
  try {
    const parsed = new URL(url);
    return `${parsed.protocol}//${parsed.host}${parsed.pathname}`;
  } catch {
    return "<invalid-url>";
  }
}

export function sanitizeCredentialText(value: string): string {
  return value
    .replace(
      /\b(?:https?|wss?):\/\/[^\s"'<>]+/gi,
      (url) => redactURL(url),
    )
    .replace(
      /([?&][A-Za-z0-9_.~-]+)=([^&#\s"'<>]*)/g,
      "$1=<redacted>",
    )
    .replace(
      /\b(ticket|token|access_token|authorization|auth|session|secret|api_key)\s*([=:])\s*[^\s,;"']+/gi,
      "$1$2<redacted>",
    );
}

export function sanitizeEndpointErrorText(value: unknown, endpoint: string): string {
  let text = sanitizeCredentialText(value instanceof Error ? value.message : String(value))
    .replace(
      /(["']?(?:ticket|token|access_token|authorization|auth|session|secret|api_key)["']?\s*:\s*["']?)[^"',}\s]+/gi,
      "$1<redacted>",
    );

  try {
    const parsed = new URL(endpoint);
    const values = [parsed.username, parsed.password];
    for (const value of parsed.searchParams.values()) {
      values.push(value, encodeURIComponent(value));
    }
    for (const raw of parsed.search.slice(1).split("&")) {
      const equals = raw.indexOf("=");
      if (equals >= 0) {
        values.push(raw.slice(equals + 1));
      }
    }
    for (const secret of values) {
      if (secret !== "") {
        text = text.split(secret).join("<redacted>");
      }
    }
  } catch {
    // The endpoint itself is never included when it cannot be parsed.
  }
  return text;
}

