export interface AuthSession {
  authenticated: boolean;
  authDisabled: boolean;
}

export class AuthError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string) {
    super(code);
    this.name = 'AuthError';
    this.status = status;
    this.code = code;
  }
}

/** Resolves the hub's HTTP base from the browser WebSocket endpoint. */
export function authBaseFromWs(wsUrl: string, baseUrl = typeof window !== 'undefined' ? window.location.href : 'http://localhost/'): string {
  const url = new URL(wsUrl, baseUrl);
  url.protocol = url.protocol === 'wss:' || url.protocol === 'https:' ? 'https:' : 'http:';
  url.pathname = '';
  url.search = '';
  url.hash = '';
  return url.toString().replace(/\/$/, '');
}

async function parseAuthResponse(response: Response): Promise<AuthSession> {
  let body: unknown = null;
  try {
    body = await response.json();
  } catch {
    body = null;
  }
  if (!response.ok) {
    const code =
      typeof body === 'object' && body !== null && typeof (body as { error?: unknown }).error === 'string'
        ? (body as { error: string }).error
        : 'request_failed';
    throw new AuthError(response.status, code);
  }
  const session = (body ?? {}) as Partial<AuthSession>;
  return { authenticated: Boolean(session.authenticated), authDisabled: Boolean(session.authDisabled) };
}

export async function fetchAuthSession(baseUrl: string, signal?: AbortSignal): Promise<AuthSession> {
  return parseAuthResponse(await fetch(`${baseUrl}/auth/session`, { credentials: 'include', signal }));
}

/** Shared-password login; the hub sets an HttpOnly session cookie. */
export async function loginWithPassword(baseUrl: string, password: string, signal?: AbortSignal): Promise<AuthSession> {
  return parseAuthResponse(
    await fetch(`${baseUrl}/auth/login`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ password }),
      signal,
    }),
  );
}

export async function logoutSession(baseUrl: string, signal?: AbortSignal): Promise<AuthSession> {
  return parseAuthResponse(await fetch(`${baseUrl}/auth/logout`, { method: 'POST', credentials: 'include', signal }));
}
