//go:build comet_v3 && windows

package committee

import (
	"golang.org/x/sys/windows"
	"testing"
	"unsafe"
)

func chCostClock(t *testing.T) (func() int64, int64, string) {
	qpc := windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryPerformanceCounter")
	qpf := windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryPerformanceFrequency")
	var frequency int64
	if ok, _, err := qpf.Call(uintptr(unsafe.Pointer(&frequency))); ok == 0 {
		t.Fatal(err)
	}
	ticks := func() int64 {
		var v int64
		if ok, _, err := qpc.Call(uintptr(unsafe.Pointer(&v))); ok == 0 {
			t.Fatal(err)
		}
		return v
	}
	return ticks, frequency, "Windows QueryPerformanceCounter via x/sys/windows"
}
