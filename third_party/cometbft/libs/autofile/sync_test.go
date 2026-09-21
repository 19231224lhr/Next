package autofile

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cometbft/cometbft/libs/operationtrace"
	"github.com/stretchr/testify/require"
)

// The optional trace hook observes actual synchronous calls, without replacing
// the real file or turning off durable writes.
func syncOperations(t *testing.T) *[]string {
	t.Helper()
	var operations []string
	old := operationtrace.Observe
	operationtrace.Observe = func(stage string, _ int64, _ time.Time, _ time.Duration) {
		operations = append(operations, stage)
	}
	t.Cleanup(func() { operationtrace.Observe = old })
	return &operations
}

func TestSyncTracking(t *testing.T) {
	ops := syncOperations(t)
	af, err := OpenAutoFile(filepath.Join(t.TempDir(), "wal"))
	require.NoError(t, err)
	defer af.Close()
	require.NoError(t, af.Sync()) // new file has no durable-state assumption
	require.Equal(t, "autofile_sync_needed", (*ops)[len(*ops)-1])
	require.NoError(t, af.Sync())
	require.Equal(t, "autofile_sync_skipped", (*ops)[len(*ops)-1])
	_, err = af.Write([]byte("one"))
	require.NoError(t, err)
	require.NoError(t, af.Sync())
	require.Equal(t, "autofile_sync_needed", (*ops)[len(*ops)-1])
	require.NoError(t, af.closeFile())
	require.NoError(t, af.Sync()) // reopened handles never inherit clean state
	require.Equal(t, "autofile_sync_needed", (*ops)[len(*ops)-1])

	_, err = af.Write([]byte("two"))
	require.NoError(t, err)
	af.mtx.Lock()
	err = af.file.Close() // real failed syscall, no mock success
	af.mtx.Unlock()
	require.NoError(t, err)
	require.Error(t, af.Sync())
	require.Error(t, af.Sync()) // failure must not mark contents durable
	require.Equal(t, "autofile_sync_needed", (*ops)[len(*ops)-1])
}

func TestGroupSyncAfterBufferedFlushAndRotation(t *testing.T) {
	ops := syncOperations(t)
	path := filepath.Join(t.TempDir(), "wal")
	g, err := OpenGroup(path)
	require.NoError(t, err)
	defer g.Head.Close()
	require.NoError(t, g.FlushAndSync())
	data := bytes.Repeat([]byte("a"), 100*1024)
	_, err = g.Write(data) // exceeds bufio capacity: some bytes are already written
	require.NoError(t, err)
	g.mtx.Lock()
	err = g.headBuf.Flush()
	g.mtx.Unlock()
	require.NoError(t, err)
	require.Zero(t, g.Buffered()) // empty buffer does not mean durable file
	require.NoError(t, g.FlushAndSync())
	require.Equal(t, "autofile_sync_needed", (*ops)[len(*ops)-1])
	g.RotateFile()
	require.NoError(t, g.FlushAndSync()) // new head must be synchronized
	require.Equal(t, "autofile_sync_needed", (*ops)[len(*ops)-1])
	require.NoError(t, g.WriteLine("new"))
	require.NoError(t, g.FlushAndSync())
	require.Equal(t, "autofile_sync_needed", (*ops)[len(*ops)-1])
	got, err := os.ReadFile(path + ".000")
	require.NoError(t, err)
	require.Equal(t, data, got)
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new\n", string(got))
}

func TestConcurrentGroupWritesAndSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal")
	g, err := OpenGroup(path)
	require.NoError(t, err)
	defer g.Head.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				if err := g.WriteLine("record"); err != nil {
					errs <- err
					return
				}
				if err := g.FlushAndSync(); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, g.FlushAndSync())
	require.NoError(t, g.Head.closeFile())
	require.NoError(t, g.FlushAndSync())
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, bytes.Repeat([]byte("record\n"), 64), got)
}
