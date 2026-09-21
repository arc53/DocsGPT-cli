package spec

import (
	"net/url"
	"strings"
)

// SameOrigin reports whether two base URLs share scheme, host and port. The
// personal access token is only ever sent to the origin the user configured
// (--url, DOCSGPT_URL or the config file), never to one a suite file names:
// a bench.yaml is data, often from someone else's repository, and its
// base_url must not be able to collect the account token.
func SameOrigin(a, b string) bool {
	ua, errA := url.Parse(strings.TrimSpace(a))
	ub, errB := url.Parse(strings.TrimSpace(b))
	if errA != nil || errB != nil || ua.Host == "" || ub.Host == "" {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) &&
		strings.EqualFold(ua.Hostname(), ub.Hostname()) &&
		effectivePort(ua) == effectivePort(ub)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}
