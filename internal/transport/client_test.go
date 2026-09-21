package transport

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRPCPoolReusesBurstConnections(t *testing.T) {
	const requests = 96
	arrived := make(chan struct{}, requests)
	gates := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var accepted atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i, _ := strconv.Atoi(r.URL.Path[1:])
		arrived <- struct{}{}
		<-gates[i]
		_, _ = w.Write([]byte("ok"))
	}))
	server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			accepted.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	client := NewHTTPClient(10 * time.Second)
	pool := client.Transport.(*http.Transport).Clone()
	client.Transport = pool
	defer pool.CloseIdleConnections()
	for round := range gates {
		var wg sync.WaitGroup
		for range requests {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := client.Get(server.URL + "/" + strconv.Itoa(round))
				if err != nil {
					t.Error(err)
					return
				}
				_, err = io.Copy(io.Discard, r.Body)
				r.Body.Close()
				if err != nil {
					t.Error(err)
				}
			}()
		}
		for range requests {
			select {
			case <-arrived:
			case <-time.After(10 * time.Second):
				close(gates[round])
				wg.Wait()
				t.Fatal("burst did not reach server")
			}
		}
		close(gates[round])
		wg.Wait()
	}
	if n := accepted.Load(); n != requests {
		t.Fatalf("two completed bursts opened %d TCP connections; want reuse of %d", n, requests)
	}
}
