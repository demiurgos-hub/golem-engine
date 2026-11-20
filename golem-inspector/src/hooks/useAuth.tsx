import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { authBaseFromWs, fetchAuthSession, loginWithPassword, logoutSession, type AuthSession } from '../api/auth';
import { DEFAULT_WS_URL } from '../api/wsClient';

export type AuthStatus = 'loading' | 'authenticated' | 'unauthenticated' | 'error';

interface AuthContextValue {
  status: AuthStatus;
  session: AuthSession | null;
  error: string | null;
  login: (password: string) => Promise<void>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

function statusFrom(session: AuthSession): AuthStatus {
  return session.authenticated || session.authDisabled ? 'authenticated' : 'unauthenticated';
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const base = useMemo(() => authBaseFromWs(DEFAULT_WS_URL), []);
  const [status, setStatus] = useState<AuthStatus>('loading');
  const [session, setSession] = useState<AuthSession | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const next = await fetchAuthSession(base);
      setSession(next);
      setError(null);
      setStatus(statusFrom(next));
    } catch (err) {
      setSession(null);
      setStatus('error');
      setError(err instanceof Error ? err.message : 'Auth status failed');
    }
  }, [base]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const login = useCallback(
    async (password: string) => {
      const next = await loginWithPassword(base, password);
      setSession(next);
      setError(null);
      setStatus(statusFrom(next));
    },
    [base],
  );

  const logout = useCallback(async () => {
    try {
      const next = await logoutSession(base);
      setSession(next);
      setStatus(statusFrom(next));
    } catch {
      setSession({ authenticated: false, authDisabled: false });
      setStatus('unauthenticated');
    }
  }, [base]);

  const value = useMemo(() => ({ status, session, error, login, logout, refresh }), [status, session, error, login, logout, refresh]);
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used within AuthProvider');
  return ctx;
}
