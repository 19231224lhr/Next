package main

// finalObservation avoids decoding an unchanged coin at every polling tick.
// Each payment owns its checker; height is published by the shared follower.
type finalObservation struct {
	checked bool
	height  int64
}

func (o *finalObservation) Check(height int64, query func() (bool, error)) (bool, error) {
	if o.checked && height == o.height {
		return false, nil
	}
	final, err := query()
	if err == nil {
		o.checked = true
		o.height = height
	} // Capture the height BEFORE the read.
	return final, err
}

// Checks run in member order, so one cursor remembers the completed prefix.
type memberObservation struct{ next int }

func (o *memberObservation) Check(count int, query func(int) (bool, error)) (bool, error) {
	for o.next < count {
		ok, err := query(o.next)
		if err != nil || !ok {
			return false, err
		}
		o.next++
	}
	return true, nil
}
