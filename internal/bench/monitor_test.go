package bench

import (
	"sync"
	"testing"
	"time"
)

func TestMonitorKeepsPeak(t *testing.T) {
	reached := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	m := StartMonitor(func() (uint64, bool) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		v := uint64(calls)
		if v == 3 {
			close(reached)
		}
		return v, true
	}, time.Millisecond)
	defer m.Stop()

	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not sample three times")
	}
	peak, known := m.Stop()
	if !known || peak < 3 {
		t.Fatalf("peak = %d (known %v)", peak, known)
	}
}

func TestMonitorUnknownAndIdempotentStop(t *testing.T) {
	m := StartMonitor(func() (uint64, bool) { return 0, false }, time.Millisecond)
	peak, known := m.Stop()
	if known || peak != 0 {
		t.Fatalf("peak = %d (known %v)", peak, known)
	}
	if peak, known := m.Stop(); known || peak != 0 {
		t.Fatalf("second stop = %d (known %v)", peak, known)
	}
}

func TestMonitorNilSampler(t *testing.T) {
	m := StartMonitor(nil, 0)
	if _, known := m.Stop(); known {
		t.Fatal("nil sampler reported a value")
	}
}
