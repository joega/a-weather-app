// Package providerhttp owns bounded, reusable provider connection pools.
package providerhttp

import (
	"net/http"
	"time"
)

// New clones configuration once, before use; it never mutates the base. Direct
// pools ignore environment proxies (IP geolocation must not disclose them).
// Owners close idle connections on shutdown. In-flight requests remain valid.
func New(base *http.Transport, direct bool) *http.Transport {
	t := base.Clone()
	t.MaxIdleConns = 8
	t.MaxIdleConnsPerHost = 2
	t.MaxConnsPerHost = 8
	t.IdleConnTimeout = 90 * time.Second
	if direct {
		t.Proxy = nil
	}
	return t
}
