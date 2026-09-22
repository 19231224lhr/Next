package protocol

import lru "github.com/hashicorp/golang-lru/v2"

// Only immutable, successful descriptor signatures live here. Dynamic payment
// authorization, routing policy and input/budget state are always checked anew.
var verifiedReceiveDescriptors = func() *lru.Cache[ReceiveDescriptor, struct{}] {
	c, err := lru.New[ReceiveDescriptor, struct{}](1024)
	if err != nil {
		panic(err)
	}
	return c
}()
