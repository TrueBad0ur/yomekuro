// Package sysstats samples host CPU/RAM/GPU metrics from procfs/sysfs. These
// paths are visible read-only inside an ordinary (non-privileged) container
// without any extra docker-compose mounts — procfs and sysfs are shared with
// the host by default, only /dev device *nodes* need explicit --device
// passthrough, which this package never touches (it only reads files).
package sysstats

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Sample struct {
	Time           time.Time `json:"time"`
	CPUPercent     float64   `json:"cpu_percent"`
	CPUTempC       *float64  `json:"cpu_temp_c,omitempty"`
	RAMUsedBytes   uint64    `json:"ram_used_bytes"`
	RAMTotalBytes  uint64    `json:"ram_total_bytes"`
	GPUTempC       *float64  `json:"gpu_temp_c,omitempty"`
	GPUBusyPercent *float64  `json:"gpu_busy_percent,omitempty"`
	VRAMUsedBytes  *uint64   `json:"vram_used_bytes,omitempty"`
	VRAMTotalBytes *uint64   `json:"vram_total_bytes,omitempty"`
}

// Collector samples the host periodically into a bounded in-memory ring
// buffer. Ephemeral by design — a restart losing history is fine, this is
// live telemetry, not something worth persisting to Postgres.
type Collector struct {
	interval time.Duration
	capacity int

	mu      sync.Mutex
	history []Sample

	prevTotal, prevIdle uint64
	prevOK              bool
}

func NewCollector(interval time.Duration, retain time.Duration) *Collector {
	capacity := int(retain/interval) + 1
	if capacity < 1 {
		capacity = 1
	}
	return &Collector{interval: interval, capacity: capacity}
}

// Run samples until ctx-like stop via the done channel; call as `go c.Run(nil)`.
func (c *Collector) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	c.sampleOnce()
	for {
		select {
		case <-ticker.C:
			c.sampleOnce()
		case <-stop:
			return
		}
	}
}

func (c *Collector) sampleOnce() {
	s := Sample{Time: time.Now()}

	if total, idle, ok := readCPUJiffies(); ok {
		c.mu.Lock()
		if c.prevOK && total > c.prevTotal {
			deltaTotal := total - c.prevTotal
			deltaIdle := idle - c.prevIdle
			if deltaTotal > 0 {
				s.CPUPercent = 100 * (1 - float64(deltaIdle)/float64(deltaTotal))
			}
		}
		c.prevTotal, c.prevIdle, c.prevOK = total, idle, true
		c.mu.Unlock()
	}

	if used, total, ok := readMemInfo(); ok {
		s.RAMUsedBytes, s.RAMTotalBytes = used, total
	}

	if t, ok := readHwmonTemp("k10temp"); ok {
		s.CPUTempC = &t
	}

	if card, ok := findDiscreteGPUCard(); ok {
		// Read the temp from the *same physical device* as the card chosen
		// above, not just any hwmon chip named "amdgpu" — a laptop APU
		// exposes both the discrete GPU and its integrated GPU under that
		// same chip name, and picking the wrong one silently reports the
		// (much cooler, idle) iGPU's temperature instead.
		if t, ok := discreteGPUHwmonTemp(card); ok {
			s.GPUTempC = &t
		}
		if busy, ok := readUintFile(filepath.Join(card, "gpu_busy_percent")); ok {
			f := float64(busy)
			s.GPUBusyPercent = &f
		}
		if used, ok := readUintFile(filepath.Join(card, "mem_info_vram_used")); ok {
			s.VRAMUsedBytes = &used
		}
		if total, ok := readUintFile(filepath.Join(card, "mem_info_vram_total")); ok {
			s.VRAMTotalBytes = &total
		}
	}

	c.mu.Lock()
	c.history = append(c.history, s)
	if len(c.history) > c.capacity {
		c.history = c.history[len(c.history)-c.capacity:]
	}
	c.mu.Unlock()
}

// Latest returns the most recent sample, or the zero value if none yet.
func (c *Collector) Latest() Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.history) == 0 {
		return Sample{}
	}
	return c.history[len(c.history)-1]
}

// History returns a copy of all retained samples, oldest first.
func (c *Collector) History() []Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Sample, len(c.history))
	copy(out, c.history)
	return out
}

// ── /proc, /sys readers ──────────────────────────────────────────────────

// readCPUJiffies parses the aggregate "cpu " line of /proc/stat: user, nice,
// system, idle, iowait, irq, softirq, steal — total is the sum of all eight,
// idle is idle+iowait. Percent-busy is computed by the caller from the delta
// between two readings, matching the standard /proc/stat CPU% technique.
func readCPUJiffies() (total, idle uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, false
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 9 || fields[0] != "cpu" {
		return 0, 0, false
	}
	vals := make([]uint64, 8)
	for i := 0; i < 8; i++ {
		v, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		vals[i] = v
		total += v
	}
	idle = vals[3] + vals[4] // idle + iowait
	return total, idle, true
}

func readMemInfo() (used, total uint64, ok bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	var memTotal, memAvailable uint64
	haveTotal, haveAvail := false, false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			memTotal = parseMeminfoKB(line)
			haveTotal = true
		case strings.HasPrefix(line, "MemAvailable:"):
			memAvailable = parseMeminfoKB(line)
			haveAvail = true
		}
		if haveTotal && haveAvail {
			break
		}
	}
	if !haveTotal || !haveAvail {
		return 0, 0, false
	}
	return (memTotal - memAvailable) * 1024, memTotal * 1024, true
}

func parseMeminfoKB(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, _ := strconv.ParseUint(fields[1], 10, 64)
	return v
}

// readHwmonTemp finds the hwmon device whose "name" file matches chipName
// (e.g. "k10temp", "amdgpu") and returns its first temp*_input, in °C.
func readHwmonTemp(chipName string) (float64, bool) {
	entries, err := os.ReadDir("/sys/class/hwmon")
	if err != nil {
		return 0, false
	}
	for _, e := range entries {
		dir := filepath.Join("/sys/class/hwmon", e.Name())
		name, err := os.ReadFile(filepath.Join(dir, "name"))
		if err != nil || strings.TrimSpace(string(name)) != chipName {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		if len(matches) == 0 {
			continue
		}
		if v, ok := readUintFile(matches[0]); ok {
			return float64(v) / 1000, true
		}
	}
	return 0, false
}

// discreteGPUHwmonTemp reads the temperature reported by the hwmon chip tied
// to the exact same PCI device as cardDevice (an /sys/class/drm/cardN/device
// path) — resolved by comparing realpath(cardDevice) against
// realpath(hwmonN/device), not by chip name, since a laptop APU exposes both
// the discrete and integrated GPU under the identical "amdgpu" chip name.
// Prefers the "junction" (hotspot) sensor over "edge" when both exist, since
// junction is the more representative reading under load; falls back to
// whichever temp*_input is present and actually readable — a discrete GPU
// in a runtime-suspended power state can have every temp*_input on its own
// chip return EINVAL, and that must surface as "no data" rather than
// silently substituting a different physical device's reading.
func discreteGPUHwmonTemp(cardDevice string) (float64, bool) {
	cardReal, err := filepath.EvalSymlinks(cardDevice)
	if err != nil {
		return 0, false
	}

	entries, err := os.ReadDir("/sys/class/hwmon")
	if err != nil {
		return 0, false
	}
	for _, e := range entries {
		dir := filepath.Join("/sys/class/hwmon", e.Name())
		hwmonReal, err := filepath.EvalSymlinks(filepath.Join(dir, "device"))
		if err != nil || hwmonReal != cardReal {
			continue
		}

		matches, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		var junction, fallback string
		for _, m := range matches {
			label, _ := os.ReadFile(strings.TrimSuffix(m, "_input") + "_label")
			if strings.TrimSpace(string(label)) == "junction" {
				junction = m
			} else if fallback == "" {
				fallback = m
			}
		}
		for _, m := range []string{junction, fallback} {
			if m == "" {
				continue
			}
			if v, ok := readUintFile(m); ok {
				return float64(v) / 1000, true
			}
		}
		return 0, false
	}
	return 0, false
}

// findDiscreteGPUCard returns the /sys/class/drm/cardN/device directory with
// the largest mem_info_vram_total — on a laptop with an integrated + discrete
// GPU, the discrete one is what actually runs mokuro's OCR and is what "GPU
// load" should mean here.
func findDiscreteGPUCard() (string, bool) {
	matches, err := filepath.Glob("/sys/class/drm/card*/device/mem_info_vram_total")
	if err != nil || len(matches) == 0 {
		return "", false
	}
	best := ""
	var bestTotal uint64
	for _, m := range matches {
		total, ok := readUintFile(m)
		if !ok {
			continue
		}
		if total > bestTotal {
			bestTotal = total
			best = filepath.Dir(m)
		}
	}
	return best, best != ""
}

func readUintFile(path string) (uint64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
