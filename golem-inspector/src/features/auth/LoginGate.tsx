import { useState, type FormEvent, type ReactNode } from 'react';
import { AuthError } from '../../api/auth';
import { useAuth } from '../../hooks/useAuth';

function LoginForm() {
  const { login } = useAuth();
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const onSubmit = async (event: FormEvent) => {
    event.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await login(password);
      setPassword('');
    } catch (err) {
      if (err instanceof AuthError && err.code === 'invalid_credentials') setError('Invalid password');
      else if (err instanceof AuthError && err.code === 'rate_limited') setError('Too many attempts; try again later');
      else if (err instanceof AuthError && err.code === 'origin_not_allowed') setError('Login blocked by origin policy');
      else setError('Login failed');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="login-gate">
      <form className="login-card" onSubmit={onSubmit}>
        <p className="brand-eyebrow">Golem Engine</p>
        <h1 className="login-title">Inspector</h1>
        <p className="muted login-copy">Enter the shared team password to continue.</p>
        <label className="login-label" htmlFor="inspector-password">
          Password
        </label>
        <input
          id="inspector-password"
          className="login-input"
          type="password"
          name="password"
          autoComplete="current-password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          disabled={submitting}
          required
        />
        {error ? <p className="danger login-error">{error}</p> : null}
        <button className="login-submit" type="submit" disabled={submitting}>
          {submitting ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  );
}

/** Blocks the UI (and the WebSocket) until the session is valid or auth is off. */
export function LoginGate({ children }: { children: ReactNode }) {
  const { status, error } = useAuth();
  if (status === 'loading') {
    return (
      <div className="login-gate">
        <p className="muted">Checking session…</p>
      </div>
    );
  }
  if (status === 'error') {
    return (
      <div className="login-gate">
        <p className="danger">{error ?? 'Unable to check authentication'}</p>
      </div>
    );
  }
  if (status === 'unauthenticated') return <LoginForm />;
  return children;
}
