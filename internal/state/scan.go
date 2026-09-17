package state

// ScanView is used by bounded maintenance queues, never to infer absence of a
// spend from an event stream. Results are exclusive of After and copied.
type Entry struct{ Key, Value []byte }
type ScanView interface {
	Scan(prefix, after []byte, limit int) ([]Entry, error)
}
