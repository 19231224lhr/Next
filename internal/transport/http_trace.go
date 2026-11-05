package transport

import (
	"net/http"
	"net/http/httptrace"
	"utxo/internal/requesttrace"
)

// Standard-library events are diagnostic only; no transport settings change.
func traceMemberRequest(r *http.Request, node string) *http.Request {
	ctx := r.Context()
	mark := func(stage string) { requesttrace.MarkNode(ctx, node, stage) }
	trace := &httptrace.ClientTrace{
		GetConn:      func(string) { mark("http_get_conn") },
		ConnectStart: func(string, string) { mark("http_connect_start") },
		ConnectDone: func(_, _ string, err error) {
			if err != nil {
				mark("http_connect_error")
			} else {
				mark("http_connect_done")
			}
		},
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Reused {
				mark("http_got_conn_reused")
			} else {
				mark("http_got_conn_fresh")
			}
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err != nil {
				mark("http_write_error")
			} else {
				mark("http_request_written")
			}
		},
		GotFirstResponseByte: func() { mark("http_first_byte") },
	}
	return r.WithContext(httptrace.WithClientTrace(ctx, trace))
}
