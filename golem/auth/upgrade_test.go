package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/auth"
)

func TestUpgradeHandlerDefaultTokenParam(t *testing.T) {
	t.Parallel()
	var saw string
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		Validate: func(_ *http.Request, token string) (any, error) {
			saw = token
			return "ok", nil
		},
	})
	r := httptest.NewRequest(http.MethodGet, "/ws?token=abc", nil)
	data, err := h(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != "ok" {
		t.Fatalf("data = %v, want ok", data)
	}
	if saw != "abc" {
		t.Fatalf("token = %q, want abc", saw)
	}
}

func TestUpgradeHandlerCustomTokenParam(t *testing.T) {
	t.Parallel()
	var saw string
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		TokenParam: "  access_token  ",
		Validate: func(_ *http.Request, token string) (any, error) {
			saw = token
			return 42, nil
		},
	})
	r := httptest.NewRequest(http.MethodGet, "/ws?access_token=xyz&token=ignored", nil)
	data, err := h(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != 42 {
		t.Fatalf("data = %v, want 42", data)
	}
	if saw != "xyz" {
		t.Fatalf("token = %q, want xyz", saw)
	}
}

func TestUpgradeHandlerWhitespaceTokenParamDefaults(t *testing.T) {
	t.Parallel()
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		TokenParam: " \t ",
		Validate: func(_ *http.Request, token string) (any, error) {
			return token, nil
		},
	})
	r := httptest.NewRequest(http.MethodGet, "/ws?token=from-default", nil)
	data, err := h(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != "from-default" {
		t.Fatalf("data = %v, want from-default", data)
	}
}

func TestUpgradeHandlerMissingEmptyWhitespaceToken(t *testing.T) {
	t.Parallel()
	called := false
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		Validate: func(*http.Request, string) (any, error) {
			called = true
			return nil, nil
		},
	})

	cases := []struct {
		name string
		raw  string
	}{
		{name: "missing", raw: "/ws"},
		{name: "empty", raw: "/ws?token="},
		{name: "space", raw: "/ws?token=" + url.QueryEscape("   ")},
		{name: "tab", raw: "/ws?token=" + url.QueryEscape("\t")},
		{name: "nbsp", raw: "/ws?token=" + url.QueryEscape("\u00a0")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			r := httptest.NewRequest(http.MethodGet, tc.raw, nil)
			_, err := h(r)
			if !errors.Is(err, auth.ErrMissingToken) {
				t.Fatalf("err = %v, want errors.Is ErrMissingToken", err)
			}
			if !strings.Contains(err.Error(), `"token"`) {
				t.Fatalf("error should name TokenParam: %v", err)
			}
			if errors.Is(err, auth.ErrInvalidRequest) {
				t.Fatal("missing credentials must not report ErrInvalidRequest")
			}
			if called {
				t.Fatal("Validate must not be called for malformed token input")
			}
		})
	}
}

func TestUpgradeHandlerDuplicateQueryUsesFirst(t *testing.T) {
	t.Parallel()
	var saw string
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		Validate: func(_ *http.Request, token string) (any, error) {
			saw = token
			return nil, nil
		},
	})
	r := httptest.NewRequest(http.MethodGet, "/ws?token=first&token=second", nil)
	if _, err := h(r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saw != "first" {
		t.Fatalf("token = %q, want first", saw)
	}
}

func TestUpgradeHandlerValidatorSuccessData(t *testing.T) {
	t.Parallel()
	type sessionData struct {
		UserID int
	}
	want := sessionData{UserID: 7}
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		Validate: func(*http.Request, string) (any, error) {
			return want, nil
		},
	})
	r := httptest.NewRequest(http.MethodGet, "/ws?token=ok", nil)
	data, err := h(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := data.(sessionData)
	if !ok || got != want {
		t.Fatalf("data = %#v, want %#v", data, want)
	}
}

func TestUpgradeHandlerValidatorSeesOriginalRequest(t *testing.T) {
	t.Parallel()
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		Validate: func(r *http.Request, token string) (any, error) {
			if token != "tok" {
				t.Fatalf("token = %q, want tok", token)
			}
			if got := r.URL.Query().Get("char_id"); got != "99" {
				t.Fatalf("char_id = %q, want 99", got)
			}
			if got := r.Header.Get("X-Test"); got != "1" {
				t.Fatalf("X-Test = %q, want 1", got)
			}
			return r.URL.Query().Get("char_id"), nil
		},
	})
	r := httptest.NewRequest(http.MethodGet, "/ws?token=tok&char_id=99", nil)
	r.Header.Set("X-Test", "1")
	data, err := h(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != "99" {
		t.Fatalf("data = %v, want 99", data)
	}
}

func TestUpgradeHandlerDoesNotMutateRequest(t *testing.T) {
	t.Parallel()
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		Validate: func(r *http.Request, _ string) (any, error) {
			if r.Form != nil {
				t.Fatal("Validate saw mutated Form")
			}
			return nil, nil
		},
	})
	r := httptest.NewRequest(http.MethodGet, "/ws?token=abc", nil)
	if _, err := h(r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Form != nil {
		t.Fatal("handler mutated Request.Form")
	}
}

func TestUpgradeHandlerValidatorSentinelErrorsIs(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("game: revoked")
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		Validate: func(*http.Request, string) (any, error) {
			return nil, sentinel
		},
	})
	r := httptest.NewRequest(http.MethodGet, "/ws?token=bad", nil)
	_, err := h(r)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want errors.Is sentinel", err)
	}
	if strings.Contains(err.Error(), "bad") {
		t.Fatalf("wrapped error leaked token: %v", err)
	}
}

func TestUpgradeHandlerNilRequest(t *testing.T) {
	t.Parallel()
	called := false
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		Validate: func(*http.Request, string) (any, error) {
			called = true
			return nil, nil
		},
	})
	_, err := h(nil)
	if !errors.Is(err, auth.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if errors.Is(err, auth.ErrMissingToken) {
		t.Fatal("nil request must not report ErrMissingToken")
	}
	if called {
		t.Fatal("Validate must not be called for nil request")
	}
}

func TestUpgradeHandlerNilValidate(t *testing.T) {
	t.Parallel()
	h := auth.UpgradeHandler(auth.UpgradeOptions{})
	r := httptest.NewRequest(http.MethodGet, "/ws?token=abc", nil)
	_, err := h(r)
	if !errors.Is(err, auth.ErrNilValidate) {
		t.Fatalf("err = %v, want ErrNilValidate", err)
	}
}

func TestUpgradeHandlerNilURL(t *testing.T) {
	t.Parallel()
	called := false
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		Validate: func(*http.Request, string) (any, error) {
			called = true
			return nil, nil
		},
	})
	r := &http.Request{Method: http.MethodGet}
	_, err := h(r)
	if !errors.Is(err, auth.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if errors.Is(err, auth.ErrMissingToken) {
		t.Fatal("nil URL must not report ErrMissingToken")
	}
	if called {
		t.Fatal("Validate must not be called when URL is nil")
	}
}

func TestUpgradeHandlerTrimsTokenWhitespace(t *testing.T) {
	t.Parallel()
	var saw string
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		Validate: func(_ *http.Request, token string) (any, error) {
			saw = token
			return token, nil
		},
	})
	raw := "\t \u00a0secret-token\u00a0 \n"
	r := httptest.NewRequest(http.MethodGet, "/ws?token="+url.QueryEscape(raw), nil)
	data, err := h(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saw != "secret-token" {
		t.Fatalf("Validate token = %q, want secret-token", saw)
	}
	if data != "secret-token" {
		t.Fatalf("data = %v, want secret-token", data)
	}
}

func TestUpgradeHandlerErrorOmitsToken(t *testing.T) {
	t.Parallel()
	secret := "super-secret-token-value"
	h := auth.UpgradeHandler(auth.UpgradeOptions{
		Validate: func(*http.Request, string) (any, error) {
			return nil, errors.New("denied")
		},
	})
	r := httptest.NewRequest(http.MethodGet, "/ws?token="+url.QueryEscape(secret), nil)
	_, err := h(r)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked token: %v", err)
	}
}
