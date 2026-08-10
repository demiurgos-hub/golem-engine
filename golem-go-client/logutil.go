package golemclient

import "net/url"

// RedactURL returns scheme, host, and path without userinfo or query values.
func RedactURL(raw string) string {
	if raw == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	redacted := parsed.String()
	if redacted == "" {
		return "<invalid-url>"
	}
	return redacted
}
