// Package sysinfo reads Linux system telemetry from /proc and /sys.
//
// The expensive/driver-specific work lives behind Backend so a different
// platform or a fake can replace it in tests and in the TUI.
package sysinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const gib = 1 << 30

// GPU is one DRM card. Fields that cannot be read are left zero and the
// matching Has* flag is false.
type GPU struct {
	Card        string
	Name        string
	Driver      string
	TempC       int
	HasTemp     bool
	VRAMTotal   uint64
	VRAMUsed    uint64
	HasVRAM     bool
	BusyPercent int
	HasBusy     bool
	PowerW      float64
	HasPower    bool
	FanRPM      int
	HasFan      bool
	PCIID       string
}

// VRAMPercent is used/total, or 0 when unknown.
func (g GPU) VRAMPercent() float64 {
	if !g.HasVRAM || g.VRAMTotal == 0 {
		return 0
	}
	return float64(g.VRAMUsed) / float64(g.VRAMTotal) * 100
}

// Snapshot is one sample of machine state.
type Snapshot struct {
	At           time.Time
	CPUs         int
	CPUPercent   float64
	MemTotal     uint64
	MemAvailable uint64
	SwapTotal    uint64
	SwapFree     uint64
	Load         [3]float64
	Uptime       time.Duration
	GPUs         []GPU
}

// MemUsed is total minus available, saturating at zero.
func (s Snapshot) MemUsed() uint64 {
	if s.MemAvailable >= s.MemTotal {
		return 0
	}
	return s.MemTotal - s.MemAvailable
}

// MemPercent is used/total, or 0 when total is unknown.
func (s Snapshot) MemPercent() float64 {
	if s.MemTotal == 0 {
		return 0
	}
	return float64(s.MemUsed()) / float64(s.MemTotal) * 100
}

// Primary returns the GPU with the most VRAM, which is normally the discrete
// card when an iGPU is also present. The bool is false when there is no GPU.
func (s Snapshot) Primary() (GPU, bool) {
	if len(s.GPUs) == 0 {
		return GPU{}, false
	}
	best := s.GPUs[0]
	for _, g := range s.GPUs[1:] {
		if g.VRAMTotal > best.VRAMTotal {
			best = g
		}
	}
	return best, true
}

// Backend produces telemetry samples.
type Backend interface {
	Sample() (Snapshot, error)
}

type cpuTimes struct {
	total uint64
	idle  uint64
}

// Collector is the Linux backend. It keeps only the previous CPU counters
// needed to turn two cumulative samples into a usage percentage.
type Collector struct {
	mu       sync.Mutex
	prevCPU  cpuTimes
	prevAt   time.Time
	SysRoot  string
	ProcRoot string
}

// NewCollector returns a collector rooted at the real /sys and /proc.
func NewCollector() *Collector {
	return &Collector{SysRoot: "/sys", ProcRoot: "/proc"}
}

// Sample reads whatever is available. A missing GPU or an unreadable sensor is
// not an error: it is normal on machines without amdgpu.
func (c *Collector) Sample() (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	s := Snapshot{At: now, CPUs: runtime.NumCPU()}

	if v, ok := readUint(filepath.Join(c.ProcRoot, "meminfo"), "MemTotal"); ok {
		s.MemTotal = v * 1024
	}
	if v, ok := readUint(filepath.Join(c.ProcRoot, "meminfo"), "MemAvailable"); ok {
		s.MemAvailable = v * 1024
	}
	if v, ok := readUint(filepath.Join(c.ProcRoot, "meminfo"), "SwapTotal"); ok {
		s.SwapTotal = v * 1024
	}
	if v, ok := readUint(filepath.Join(c.ProcRoot, "meminfo"), "SwapFree"); ok {
		s.SwapFree = v * 1024
	}
	if loads, err := readLoadAvg(filepath.Join(c.ProcRoot, "loadavg")); err == nil {
		s.Load = loads
	}
	if secs, err := readUptime(filepath.Join(c.ProcRoot, "uptime")); err == nil {
		s.Uptime = secs
	}
	if cur, err := readCPUTimes(filepath.Join(c.ProcRoot, "stat")); err == nil {
		if c.prevAt.IsZero() {
			s.CPUPercent = 0
		} else {
			dt := cur.total - c.prevCPU.total
			di := cur.idle - c.prevCPU.idle
			if dt > 0 && di <= dt {
				s.CPUPercent = float64(dt-di) / float64(dt) * 100
			}
		}
		c.prevCPU, c.prevAt = cur, now
	}

	s.GPUs = scanGPUs(c.SysRoot)
	return s, nil
}

// History keeps a bounded window of series for the graphs.
type History struct {
	Max   int
	CPU   []float64
	Mem   []float64
	Temp  []float64
	VRAM  []float64
	Busy  []float64
	Power []float64
}

func NewHistory(max int) *History {
	if max <= 0 {
		max = 120
	}
	return &History{Max: max}
}

// Push appends a sample, trimming the oldest entries.
func (h *History) Push(s Snapshot) {
	h.CPU = push(h.CPU, s.CPUPercent, h.Max)
	h.Mem = push(h.Mem, s.MemPercent(), h.Max)
	g, ok := s.Primary()
	if ok {
		if g.HasTemp {
			h.Temp = push(h.Temp, float64(g.TempC), h.Max)
		}
		if g.HasVRAM {
			h.VRAM = push(h.VRAM, g.VRAMPercent(), h.Max)
		}
		if g.HasBusy {
			h.Busy = push(h.Busy, float64(g.BusyPercent), h.Max)
		}
		if g.HasPower {
			h.Power = push(h.Power, g.PowerW, h.Max)
		}
	}
}

func push(s []float64, v float64, max int) []float64 {
	s = append(s, v)
	if len(s) > max {
		s = s[len(s)-max:]
	}
	return s
}

// scanGPUs finds cardN devices under root/class/drm. It deliberately ignores
// connector entries such as card1-DP-1.
func scanGPUs(root string) []GPU {
	matches, _ := filepath.Glob(filepath.Join(root, "class", "drm", "card[0-9]*"))
	var gpus []GPU
	for _, cardDir := range matches {
		card := filepath.Base(cardDir)
		if !isCardName(card) {
			continue
		}
		dev := filepath.Join(cardDir, "device")
		if info, err := os.Stat(dev); err != nil || !info.IsDir() {
			continue
		}
		g := GPU{Card: card, Name: "GPU " + card}
		g.Driver = readTrim(filepath.Join(dev, "uevent"), "DRIVER=")
		g.PCIID = readTrim(filepath.Join(dev, "uevent"), "PCI_ID=")
		if name := strings.TrimSpace(readFile(filepath.Join(dev, "product_name"))); name != "" {
			g.Name = name
		}

		if v, ok := readSysUint(dev, "mem_info_vram_total"); ok && v > 0 {
			g.VRAMTotal = v
			if used, ok := readSysUint(dev, "mem_info_vram_used"); ok {
				g.VRAMUsed = used
				g.HasVRAM = true
			}
		}
		if v, ok := readSysUint(dev, "gpu_busy_percent"); ok {
			g.BusyPercent = int(v)
			g.HasBusy = true
		}

		hwmons, _ := filepath.Glob(filepath.Join(dev, "hwmon", "hwmon*"))
		sort.Strings(hwmons)
		for _, hw := range hwmons {
			if !g.HasTemp {
				if v, ok := readSysInt(hw, "temp1_input"); ok {
					g.TempC = int(v / 1000)
					g.HasTemp = true
				}
			}
			if !g.HasFan {
				if v, ok := readSysInt(hw, "fan1_input"); ok {
					g.FanRPM = int(v)
					g.HasFan = true
				}
			}
			if !g.HasPower {
				if v, ok := readSysInt(hw, "power1_average"); ok {
					g.PowerW = float64(v) / 1_000_000
					g.HasPower = true
				}
			}
		}
		if !g.HasTemp {
			if v, ok := readSysInt(dev, "temp1_input"); ok {
				g.TempC = int(v / 1000)
				g.HasTemp = true
			}
		}
		gpus = append(gpus, g)
	}
	sort.Slice(gpus, func(i, j int) bool { return gpus[i].Card < gpus[j].Card })
	return gpus
}

func isCardName(name string) bool {
	if len(name) <= 4 || !strings.HasPrefix(name, "card") {
		return false
	}
	for _, r := range name[4:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// readTrim looks for a key=value line in a sysfs uevent file.
func readTrim(path, prefix string) string {
	for _, line := range strings.Split(readFile(path), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func readSysUint(dir, name string) (uint64, bool) {
	v, ok := readIntFile(filepath.Join(dir, name))
	if !ok || v < 0 {
		return 0, false
	}
	return uint64(v), true
}

func readSysInt(dir, name string) (int64, bool) {
	return readIntFile(filepath.Join(dir, name))
}

func readIntFile(path string) (int64, bool) {
	s := strings.TrimSpace(readFile(path))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// readUint finds "Key: 12345 kB" in /proc/meminfo and returns 12345.
func readUint(path, key string) (uint64, bool) {
	for _, line := range strings.Split(readFile(path), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.TrimSuffix(fields[0], ":") != key {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

func readLoadAvg(path string) ([3]float64, error) {
	var out [3]float64
	fields := strings.Fields(readFile(path))
	if len(fields) < 3 {
		return out, fmt.Errorf("loadavg: short file")
	}
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return out, err
		}
		out[i] = v
	}
	return out, nil
}

func readUptime(path string) (time.Duration, error) {
	fields := strings.Fields(readFile(path))
	if len(fields) < 1 {
		return 0, fmt.Errorf("uptime: short file")
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(secs * float64(time.Second)), nil
}

// readCPUTimes sums the first cpu line in /proc/stat. guest/guest_nice are
// already included in user/nice, so they are not added again.
func readCPUTimes(path string) (cpuTimes, error) {
	for _, line := range strings.Split(readFile(path), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var total, idle uint64
		for i, f := range fields[1:] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return cpuTimes{}, err
			}
			total += v
			if i == 3 || i == 4 { // idle, iowait
				idle += v
			}
		}
		return cpuTimes{total: total, idle: idle}, nil
	}
	return cpuTimes{}, fmt.Errorf("stat: no cpu line")
}

// TotalVRAM sums across cards for compact footer output.
func (s Snapshot) TotalVRAM() (used, total uint64) {
	for _, g := range s.GPUs {
		used += g.VRAMUsed
		total += g.VRAMTotal
	}
	return used, total
}
