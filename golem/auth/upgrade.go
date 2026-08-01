package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// DefaultTokenParam is the query parameter name used when UpgradeOptions.TokenParam
// is empty or whitespace-only.
const DefaultTokenParam = "token"

// ErrMissingToken is returned when the configured token query parameter is
// absent, empty, or whitespace-only after TrimSpace.
var ErrMissingToken = errors.New("golem/auth: missing or empty token query parameter")

// ErrNilValidate is returned when UpgradeOptions.Validate is nil. UpgradeHandler
// does not panic; the returned handler rejects every request with this error.
var ErrNilValidate = errors.New("golem/auth: Validate is required")

// ErrInvalidRequest is returned when the upgrade handler receives a nil
// *http.Request or a request with a nil URL. This is distinct from
// ErrMissingToken (absent/blank credentials on an otherwise usable request).
// Like other handler errors, it still causes OnUpgrade to reject with HTTP 401.
var ErrInvalidRequest = errors.New("golem/auth: invalid request")

// UpgradeOptions configures UpgradeHandler.
type UpgradeOptions struct {
	// TokenParam is the query parameter that carries the auth token.
	// Empty or whitespace-only values default to DefaultTokenParam ("token").
	// Surrounding whitespace is trimmed. When the request includes duplicate
	// values for this parameter, the first value is used (url.Values.Get).
	TokenParam string

	// Validate checks the extracted non-empty token. The original *http.Request
	// is passed unchanged so games can read other query parameters (for example
	// char_id). The returned value is stored in Session.Data when used with
	// Server.OnUpgrade / Listener.OnUpgrade. Validate must be non-nil; if it is
	// nil, the handler returns ErrNilValidate for every request.
	Validate func(r *http.Request, token string) (any, error)
}

// UpgradeHandler returns a function suitable for server.OnUpgrade(...).
//
// TokenParam is trimmed; empty/whitespace defaults to "token". The handler
// rejects a nil request or nil URL with ErrInvalidRequest, rejects
// missing/blank tokens with ErrMissingToken wrapped with the TokenParam name
// (without calling Validate), and otherwise calls Validate with the original
// request and the trimmed token (ASCII/Unicode surrounding whitespace removed).
// Validate's data is returned unchanged. Validate errors are wrapped with
// fmt.Errorf %w so errors.Is still matches sentinel errors from the validator.
// Error messages may include the TokenParam name but never the token value.
//
// Nil Validate is a programmer misconfiguration. Consistent with other option
// constructors in this module (for example golem/host.Run), UpgradeHandler does
// not panic: the returned handler rejects every request with ErrNilValidate.
func UpgradeHandler(opts UpgradeOptions) func(*http.Request) (any, error) {
	param := strings.TrimSpace(opts.TokenParam)
	if param == "" {
		param = DefaultTokenParam
	}
	validate := opts.Validate

	return func(r *http.Request) (any, error) {
		if validate == nil {
			return nil, ErrNilValidate
		}
		if r == nil || r.URL == nil {
			return nil, ErrInvalidRequest
		}

		// URL.Query parses RawQuery only; it does not mutate the request.
		token := strings.TrimSpace(r.URL.Query().Get(param))
		if token == "" {
			return nil, fmt.Errorf("%w %q", ErrMissingToken, param)
		}

		data, err := validate(r, token)
		if err != nil {
			return nil, fmt.Errorf("golem/auth: %w", err)
		}
		return data, nil
	}
}
