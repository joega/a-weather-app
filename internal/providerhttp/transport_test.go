package providerhttp

import (
	"net/http"
	"testing"
	"time"
)

func TestPoolsAreBoundedAndConfigurationIsPrivate(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport)
	normal, direct := New(base, false), New(base, true)
	defer normal.CloseIdleConnections()
	defer direct.CloseIdleConnections()
	if normal == base || normal == direct || direct.Proxy != nil || normal.Proxy == nil {
		t.Fatal("pool ownership/proxy policy")
	}
	for _, pool := range []*http.Transport{normal, direct} {
		if pool.MaxIdleConns != 8 || pool.MaxIdleConnsPerHost != 2 || pool.MaxConnsPerHost != 8 || pool.IdleConnTimeout != 90*time.Second {
			t.Fatal("unbounded pool")
		}
	}
	if base.MaxIdleConns == 8 {
		t.Fatal("process default transport was mutated")
	}
}
