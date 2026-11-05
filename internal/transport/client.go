package transport

import (
	"net/http"
	"time"
)

// Share a bounded standard-library pool within each process. Retain a complete
// burst between calls instead of closing half its sockets after every wave.
var rpcTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 1024
	t.MaxIdleConnsPerHost = 128
	t.MaxConnsPerHost = 128
	return t
}()

func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: rpcTransport, Timeout: timeout}
}
