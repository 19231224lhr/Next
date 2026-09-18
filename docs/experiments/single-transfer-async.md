# Gateway response before background persistence

One 100-CAL payment from an unused finalized UTXO. New finite lab with 64 initial outputs per owner; 14 independent processes on the same Mac Studio. Previous lab was stopped. Diagnostic tracing enabled. Different history and cache conditions from prior samples; not an A/B speedup estimate.

TXCer initially verified: 21.156 ms.
Wallet READY: 36.328 ms.
Final output proof observed: 1060.992 ms.

Gateway persistence runs after full response flush and is no longer in the foreground trace. The wallet trace does not measure background completion. A blocked-storage HTTP test separately verifies the client can read and verify the complete certificate before gateway persistence returns.

| Node | Event | Offset from HTTP submit (ms) |
|---|---|---:|
| wallet | http_submit | 0.000 |
| gateway | http_handler_enter | 0.202 |
| gateway | request_decoded | 0.583 |
| gateway | fanout_start | 0.653 |
| http://127.0.0.1:20003 | http_handler_enter | 1.349 |
| http://127.0.0.1:20003 | request_decoded | 1.496 |
| http://127.0.0.1:20002 | http_handler_enter | 1.564 |
| http://127.0.0.1:20000 | http_handler_enter | 1.630 |
| http://127.0.0.1:20002 | request_decoded | 1.693 |
| http://127.0.0.1:20003 | validation_complete | 1.694 |
| http://127.0.0.1:20003 | store_enter | 1.700 |
| http://127.0.0.1:20001 | http_handler_enter | 1.735 |
| http://127.0.0.1:20000 | request_decoded | 1.751 |
| http://127.0.0.1:20003 | state_checks_complete | 1.849 |
| http://127.0.0.1:20001 | request_decoded | 1.849 |
| http://127.0.0.1:20002 | validation_complete | 1.866 |
| http://127.0.0.1:20002 | store_enter | 1.874 |
| http://127.0.0.1:20000 | validation_complete | 1.918 |
| http://127.0.0.1:20000 | store_enter | 1.927 |
| http://127.0.0.1:20002 | state_checks_complete | 2.017 |
| http://127.0.0.1:20001 | validation_complete | 2.021 |
| http://127.0.0.1:20001 | store_enter | 2.026 |
| http://127.0.0.1:20000 | state_checks_complete | 2.058 |
| http://127.0.0.1:20001 | state_checks_complete | 2.164 |
| http://127.0.0.1:20003 | commit_returned | 11.289 |
| http://127.0.0.1:20003 | vote_signed | 11.312 |
| http://127.0.0.1:20003 | response_ready | 11.314 |
| gateway | vote_3_accepted | 11.549 |
| http://127.0.0.1:20002 | commit_returned | 15.223 |
| http://127.0.0.1:20002 | vote_signed | 15.241 |
| http://127.0.0.1:20002 | response_ready | 15.243 |
| gateway | vote_2_accepted | 15.450 |
| http://127.0.0.1:20001 | commit_returned | 19.208 |
| http://127.0.0.1:20000 | commit_returned | 19.210 |
| http://127.0.0.1:20001 | vote_signed | 19.225 |
| http://127.0.0.1:20000 | vote_signed | 19.227 |
| http://127.0.0.1:20000 | response_ready | 19.229 |
| http://127.0.0.1:20001 | response_ready | 19.230 |
| gateway | vote_1_accepted | 19.443 |
| gateway | quorum_collected | 19.444 |
| gateway | certificate_verified | 19.590 |
| gateway | response_ready | 19.678 |
| wallet | response_headers_received | 19.847 |
| wallet | response_body_received | 19.932 |
| wallet | certificate_verified | 21.157 |
| wallet | recipient_persisted | 36.329 |
