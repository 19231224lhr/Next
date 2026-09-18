package transport

import (
	"net/http"
	"time"
)

// Share a bounded standard-library pool within each process. The default of
// two idle connections per host churns sockets under concurrent proof polling.
var rpcTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 512
	t.MaxIdleConnsPerHost = 64
	t.MaxConnsPerHost = 128
	return t
}()

func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: rpcTransport, Timeout: timeout}
}
