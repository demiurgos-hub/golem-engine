// Package auth standardizes token-in-query-parameter authorization for
// realtime transport upgrades (golem.Server.OnUpgrade / golem/net.Listener.OnUpgrade).
//
// It is intentionally small and policy-free: extract a query token, reject
// missing or blank values, and delegate validation to the game. It does not
// parse JWTs, read Authorization headers or cookies, manage accounts or DB
// sessions, or store game state.
//
// # OnUpgrade mapping
//
// Wire the handler with:
//
//	server.OnUpgrade(auth.UpgradeHandler(auth.UpgradeOptions{
//	    Validate: func(r *http.Request, token string) (any, error) {
//	        // inspect token and other query params (e.g. char_id)
//	        return sessionData, nil
//	    },
//	}))
//
// A non-nil error rejects the upgrade with HTTP 401. The successful return
// value is stored in Session.Data before OnConnect. ErrInvalidRequest covers a
// nil request or nil URL; ErrMissingToken covers absent/blank credentials on a
// usable request (errors may name TokenParam, never the token). Validate
// implementations must also avoid putting secrets in returned errors, because
// the net listener both logs the error and writes err.Error() into the 401
// response body.
//
// # Client pairing
//
// Go clients typically append the same query parameters with golem-go-client's
// WithQueryParam (and related realtime bootstrap / ConnectOptions helpers).
// JavaScript clients use the golem-engine realtime bootstrap helpers that build
// ConnectOptions with matching query parameters. This package does not import
// those clients.
package auth
