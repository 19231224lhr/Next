package protocol

import (
	lru "github.com/hashicorp/golang-lru/v2"
	"os"
	"sync/atomic"
)

var descriptorMetricsEnabled = os.Getenv("UTXO_E8_METRICS") == "1"
var descriptorQueries, descriptorHits, descriptorVerifications atomic.Uint64

type DescriptorCounters struct{ Queries, Hits, Verifications uint64 }

func DescriptorMetrics() DescriptorCounters {
	return DescriptorCounters{descriptorQueries.Load(), descriptorHits.Load(), descriptorVerifications.Load()}
}

// Only immutable, successful descriptor signatures live here. Dynamic payment
// authorization, routing policy and input/budget state are always checked anew.
var verifiedReceiveDescriptors = func() *lru.Cache[ReceiveDescriptor, struct{}] {
	c, err := lru.New[ReceiveDescriptor, struct{}](1024)
	if err != nil {
		panic(err)
	}
	return c
}()
