package main

import (
	"testing"

	cmtcfg "github.com/cometbft/cometbft/config"
)

func TestConfigureBandwidth(t *testing.T) {
	for _, test := range []struct {
		name       string
		send, recv int64
		invalid    bool
	}{
		{"defaults", 0, 0, false},
		{"send only", 51200000, 0, false},
		{"receive only", 0, 51200000, false},
		{"both", 51200000, 51200000, false},
		{"negative send", -1, 51200000, true},
		{"negative receive", 51200000, -1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p2p := cmtcfg.DefaultP2PConfig()
			before := *p2p
			err := configureBandwidth(p2p, configuration{P2PSendRate: test.send, P2PRecvRate: test.recv})
			if test.invalid {
				if err == nil || p2p.SendRate != before.SendRate || p2p.RecvRate != before.RecvRate {
					t.Fatal("invalid configuration must fail without changing limits")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			send, recv := before.SendRate, before.RecvRate
			if test.send > 0 {
				send = test.send
			}
			if test.recv > 0 {
				recv = test.recv
			}
			if p2p.SendRate != send || p2p.RecvRate != recv {
				t.Fatalf("got %d/%d; want %d/%d bytes/s", p2p.SendRate, p2p.RecvRate, send, recv)
			}
			if p2p.FlushThrottleTimeout != before.FlushThrottleTimeout || p2p.MaxPacketMsgPayloadSize != before.MaxPacketMsgPayloadSize {
				t.Fatal("bandwidth configuration changed unrelated transport settings")
			}
		})
	}
}
