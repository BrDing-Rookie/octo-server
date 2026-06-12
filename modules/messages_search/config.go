package messages_search

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// SearchConfig holds runtime configuration for the OpenSearch-backed
// /v1/messages/_search* endpoints.
//
// TODO: lift this struct to octo-lib/config.SearchConfig once the next
// octo-lib release window opens. For now we read directly from environment
// variables to avoid coupling this feature work to an octo-lib bump.
type SearchConfig struct {
	OSAddrs     []string
	OSUsername  string
	OSPassword  string
	OSReadAlias string
	Timeout     time.Duration
	RateLimit   RateLimitCfg
	CursorHMAC  string
	// UserAvatarBaseURL, when non-empty, is prepended to the relative
	// `users/{uid}/avatar` template so the response carries an absolute
	// URL (spec v4.2 §2.1 / R8). When empty we keep the relative path and
	// rely on the frontend joining it with its own API base — see
	// docs/messages-search/FIX-2026-06-12.md for the SRE rollout note.
	UserAvatarBaseURL string
}

// RateLimitCfg drives the per-loginUID 5 QPS / 20 burst limiter.
type RateLimitCfg struct {
	QPS   float64
	Burst int
}

// loadConfig builds a SearchConfig from process environment variables.
func loadConfig() SearchConfig {
	return SearchConfig{
		OSAddrs:     splitCSV(os.Getenv("OCTO_SEARCH_OS_ADDRS"), []string{"http://localhost:9200"}),
		OSUsername:  os.Getenv("OCTO_SEARCH_OS_USERNAME"),
		OSPassword:  os.Getenv("OCTO_SEARCH_OS_PASSWORD"),
		OSReadAlias: defaultStr(os.Getenv("OCTO_SEARCH_OS_READ_ALIAS"), "wukongim-messages-read"),
		Timeout:     parseDuration(os.Getenv("OCTO_SEARCH_TIMEOUT"), 5*time.Second),
		RateLimit: RateLimitCfg{
			QPS:   parseFloat(os.Getenv("OCTO_SEARCH_RPS"), 5.0),
			Burst: parseInt(os.Getenv("OCTO_SEARCH_BURST"), 20),
		},
		CursorHMAC:        os.Getenv("OCTO_SEARCH_CURSOR_HMAC"),
		UserAvatarBaseURL: strings.TrimRight(os.Getenv("OCTO_USER_AVATAR_BASE_URL"), "/"),
	}
}

func splitCSV(v string, def []string) []string {
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func parseDuration(v string, def time.Duration) time.Duration {
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return def
}

func parseFloat(v string, def float64) float64 {
	if v == "" {
		return def
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
		return f
	}
	return def
}

func parseInt(v string, def int) int {
	if v == "" {
		return def
	}
	if i, err := strconv.Atoi(v); err == nil && i > 0 {
		return i
	}
	return def
}
