package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSampleFakeSysfs(t *testing.T) {
	tmp := t.TempDir()
	proc := filepath.Join(tmp, "proc")
	sys := filepath.Join(tmp, "sys")

	writeFile(t, filepath.Join(proc, "meminfo"), `MemTotal:       16000000 kB
MemFree:         2000000 kB
MemAvailable:    8000000 kB
SwapTotal:       4000000 kB
SwapFree:        3000000 kB
`)
	writeFile(t, filepath.Join(proc, "loadavg"), "0.50 0.75 1.00 2/345 6789\n")
	writeFile(t, filepath.Join(proc, "uptime"), "12345.67 99999.00\n")
	writeFile(t, filepath.Join(proc, "stat"), `cpu  100 0 100 800 0 0 0 0 0 0
cpu0 50 0 50 400 0 0 0 0 0 0
`)

	card1 := filepath.Join(sys, "class", "drm", "card1", "device")
	writeFile(t, filepath.Join(card1, "uevent"), "DRIVER=amdgpu\nPCI_ID=1002:73FF\n")
	writeFile(t, filepath.Join(card1, "product_name"), "AMD Radeon RX 6600\n")
	writeFile(t, filepath.Join(card1, "mem_info_vram_total"), "8589934592\n")
	writeFile(t, filepath.Join(card1, "mem_info_vram_used"), "2147483648\n")
	writeFile(t, filepath.Join(card1, "gpu_busy_percent"), "37\n")
	hw := filepath.Join(card1, "hwmon", "hwmon3")
	writeFile(t, filepath.Join(hw, "temp1_input"), "45000\n")
	writeFile(t, filepath.Join(hw, "fan1_input"), "1200\n")
	writeFile(t, filepath.Join(hw, "power1_average"), "42000000\n")

	// A connector entry must be ignored, and card0 without VRAM is still a GPU.
	writeFile(t, filepath.Join(sys, "class", "drm", "card1-DP-1", "device", "uevent"), "DRIVER=amdgpu\n")
	writeFile(t, filepath.Join(sys, "class", "drm", "card0", "device", "uevent"), "DRIVER=amdgpu\n")

	c := &Collector{SysRoot: sys, ProcRoot: proc}
	s, err := c.Sample()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.GPUs) != 2 {
		t.Fatalf("gpus = %d (%+v), want 2", len(s.GPUs), s.GPUs)
	}
	primary, ok := s.Primary()
	if !ok {
		t.Fatal("no primary GPU")
	}
	if primary.Card != "card1" || primary.Name != "AMD Radeon RX 6600" {
		t.Fatalf("primary = %+v", primary)
	}
	if primary.TempC != 45 || !primary.HasTemp {
		t.Fatalf("temp = %+v", primary)
	}
	if primary.VRAMTotal != 8*gib || primary.VRAMUsed != 2*gib {
		t.Fatalf("vram = %+v", primary)
	}
	if p := primary.VRAMPercent(); p < 24.9 || p > 25.1 {
		t.Fatalf("vram pct = %v", p)
	}
	if primary.BusyPercent != 37 || primary.FanRPM != 1200 || primary.PowerW != 42 {
		t.Fatalf("sensors = %+v", primary)
	}
	if s.MemTotal != 16000000*1024 || s.MemUsed() != 8000000*1024 {
		t.Fatalf("mem = %d/%d", s.MemUsed(), s.MemTotal)
	}
	if s.MemPercent() < 49.9 || s.MemPercent() > 50.1 {
		t.Fatalf("mem pct = %v", s.MemPercent())
	}
	if s.Load != [3]float64{0.5, 0.75, 1.0} {
		t.Fatalf("load = %v", s.Load)
	}
	if s.Uptime < 12345*time.Second {
		t.Fatalf("uptime = %v", s.Uptime)
	}

	// First sample has no baseline, the second sees 100% busy.
	writeFile(t, filepath.Join(proc, "stat"), `cpu  200 0 200 800 0 0 0 0 0 0
`)
	s2, err := c.Sample()
	if err != nil {
		t.Fatal(err)
	}
	if s2.CPUPercent < 99.9 {
		t.Fatalf("cpu pct = %v, want 100", s2.CPUPercent)
	}
}

func TestHistoryTrim(t *testing.T) {
	h := NewHistory(3)
	for i := 0; i < 5; i++ {
		h.Push(Snapshot{CPUPercent: float64(i), GPUs: []GPU{{TempC: 40 + i, HasTemp: true}}})
	}
	if len(h.CPU) != 3 || h.CPU[2] != 4 {
		t.Fatalf("cpu history = %v", h.CPU)
	}
	if len(h.Temp) != 3 || h.Temp[0] != 42 {
		t.Fatalf("temp history = %v", h.Temp)
	}
}

func TestScanGPUsMissingRoot(t *testing.T) {
	if gpus := scanGPUs(filepath.Join(t.TempDir(), "nope")); len(gpus) != 0 {
		t.Fatalf("gpus = %+v", gpus)
	}
}
