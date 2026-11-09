package bench

import (
	"sync"
	"time"
)

// Monitor samples a value in the background and keeps the peak. It is used for
// VRAM, where the interesting number is the highest point, not the last sample.
type Monitor struct {
	sample   func() (uint64, bool)
	interval time.Duration

	stop chan struct{}
	done chan struct{}
	once sync.Once

	peak  uint64
	known bool
}

// StartMonitor begins sampling. A nil sampler or a non-positive interval
// produces a monitor that reports no data.
func StartMonitor(sample func() (uint64, bool), interval time.Duration) *Monitor {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	m := &Monitor{
		sample:   sample,
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if sample != nil {
		if v, ok := sample(); ok {
			m.peak, m.known = v, true
		}
	}
	go m.loop()
	return m
}

func (m *Monitor) loop() {
	defer close(m.done)
	if m.sample == nil {
		return
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			if v, ok := m.sample(); ok {
				if !m.known || v > m.peak {
					m.peak, m.known = v, true
				}
			}
		}
	}
}

// Stop ends sampling and returns the peak. It is safe to call more than once.
func (m *Monitor) Stop() (uint64, bool) {
	m.once.Do(func() {
		close(m.stop)
		<-m.done
	})
	return m.peak, m.known
}
