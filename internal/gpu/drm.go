package gpu

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gpumon/internal/protocol"
)

// drmGPU 為一張以 sysfs 取樣的 AMD (amdgpu) 或 Intel (i915, xe) GPU.
//
// AMD 的各項讀取依 Linux 核心文件 Documentation/gpu/amdgpu/thermal.rst 與
// driver-misc.rst 所列的 sysfs 介面實作, 推斷, 未實測. Intel 的 hwmon 與 xe
// 驅動的讀取依核心文件實作, 推斷, 未實測; i915 的時脈讀取已於測試主機實測.
type drmGPU struct {
	vendor string
	driver string
	// card 為 /sys/class/drm/cardN, dev 為其 device 指向的 PCI 裝置目錄.
	card, dev string
	// pdev 為 PCI 位址, 例如 "0000:00:02.0", 與 fdinfo 的 drm-pdev 對應.
	pdev  string
	hwmon string
}

// cardName 符合 DRM 的主裝置節點名稱, 排除 card0-DP-1 這類 connector.
var cardName = regexp.MustCompile(`^card[0-9]+$`)

// openDRM 列出 sysRoot/class/drm 下由 amdgpu, i915 或 xe 驅動的 GPU, 依
// card 編號排序. NVIDIA 的 card 不在此列, 由 NVML 處理.
func openDRM(sysRoot string) ([]*drmGPU, []protocol.Device) {
	base := filepath.Join(sysRoot, "class", "drm")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, nil
	}
	type card struct {
		n    int
		name string
	}
	var cards []card
	for _, e := range entries {
		if cardName.MatchString(e.Name()) {
			n, _ := strconv.Atoi(e.Name()[len("card"):])
			cards = append(cards, card{n, e.Name()})
		}
	}
	slices.SortFunc(cards, func(a, b card) int { return a.n - b.n })

	var gpus []*drmGPU
	var devs []protocol.Device
	seen := map[string]bool{}
	for _, c := range cards {
		cardDir := filepath.Join(base, c.name)
		dev, err := filepath.EvalSymlinks(filepath.Join(cardDir, "device"))
		if err != nil {
			continue
		}
		driverLink, err := os.Readlink(filepath.Join(dev, "driver"))
		if err != nil {
			continue
		}
		g := &drmGPU{driver: filepath.Base(driverLink), card: cardDir, dev: dev, pdev: filepath.Base(dev)}
		switch g.driver {
		case "amdgpu":
			g.vendor = protocol.VendorAMD
		case "i915", "xe":
			g.vendor = protocol.VendorIntel
		default:
			continue
		}
		if seen[g.pdev] {
			continue
		}
		seen[g.pdev] = true
		g.hwmon = findHwmon(dev)
		gpus = append(gpus, g)
		devs = append(devs, g.device())
	}
	return gpus, devs
}

// device 取得此 GPU 的靜態資訊, ID 由呼叫端填入.
func (g *drmGPU) device() protocol.Device {
	codes := readPCICodes(g.dev)
	d := protocol.Device{
		Vendor:          g.vendor,
		PCIVendor:       codes.vendor,
		PCIDevice:       codes.device,
		PCISubsysVendor: codes.subsysVendor,
		PCISubsysDevice: codes.subsysDevice,
	}
	if g.vendor == protocol.VendorAMD {
		d.Name, _ = readTrim(filepath.Join(g.dev, "product_name"))
	}
	if d.Name == "" {
		d.Name = lookupPCIName(codes.vendor, codes.device)
	}
	if d.Name == "" {
		label := "AMD GPU"
		if g.vendor == protocol.VendorIntel {
			label = "Intel GPU"
		}
		d.Name = strings.TrimSpace(label + " " + codes.vendor + ":" + codes.device)
	}
	if g.hwmon != "" {
		if v, ok := firstUint(g.hwmon, "power1_cap", "power1_max"); ok && v > 0 {
			d.PowerMaxW = intPtr(int((v + 500000) / 1000000))
		}
	}
	switch g.driver {
	case "amdgpu":
		if v, ok := readUint(filepath.Join(g.dev, "mem_info_vram_total")); ok {
			d.MemTotal = &v
		}
		d.ClockGPUMax = maxDPMClock(filepath.Join(g.dev, "pp_dpm_sclk"))
		d.ClockMemMax = maxDPMClock(filepath.Join(g.dev, "pp_dpm_mclk"))
	case "i915":
		if v, ok := firstUint("", filepath.Join(g.card, "gt", "gt0", "rps_RP0_freq_mhz"), filepath.Join(g.card, "gt_RP0_freq_mhz")); ok {
			d.ClockGPUMax = intPtr(int(v))
		}
	case "xe":
		if v, ok := readUint(filepath.Join(g.dev, "tile0", "gt0", "freq0", "rp0_freq")); ok {
			d.ClockGPUMax = intPtr(int(v))
		}
	}
	return d
}

// sample 取得此 GPU 的一筆取樣, 程序列表由呼叫端另行填入.
func (g *drmGPU) sample() protocol.GPUSample {
	var s protocol.GPUSample
	if g.hwmon != "" {
		if v, ok := readInt(filepath.Join(g.hwmon, "temp1_input")); ok {
			s.Temp = intPtr(int(roundDiv(v, 1000)))
		}
		if v, ok := firstUint(g.hwmon, "power1_average", "power1_input"); ok {
			s.PowerW = intPtr(int((v + 500000) / 1000000))
		}
	}
	switch g.driver {
	case "amdgpu":
		if v, ok := readInt(filepath.Join(g.dev, "gpu_busy_percent")); ok {
			s.Util = intPtr(int(v))
		}
		if v, ok := readUint(filepath.Join(g.dev, "mem_info_vram_used")); ok {
			s.MemUsed = &v
		}
		if g.hwmon != "" {
			// freq1_input 為 GPU 時脈 (sclk), freq2_input 為記憶體時脈 (mclk), 單位為 Hz.
			if v, ok := readUint(filepath.Join(g.hwmon, "freq1_input")); ok {
				s.ClockGPU = intPtr(int((v + 500000) / 1000000))
			}
			if v, ok := readUint(filepath.Join(g.hwmon, "freq2_input")); ok {
				s.ClockMem = intPtr(int((v + 500000) / 1000000))
			}
			if pwm, ok := readUint(filepath.Join(g.hwmon, "pwm1")); ok {
				pwmMax, ok := readUint(filepath.Join(g.hwmon, "pwm1_max"))
				if !ok || pwmMax == 0 {
					pwmMax = 255
				}
				s.Fan = intPtr(int((pwm*100 + pwmMax/2) / pwmMax))
			}
		}
	case "i915":
		if v, ok := firstUint("", filepath.Join(g.card, "gt", "gt0", "rps_cur_freq_mhz"), filepath.Join(g.card, "gt_cur_freq_mhz")); ok {
			s.ClockGPU = intPtr(int(v))
		}
	case "xe":
		if v, ok := readUint(filepath.Join(g.dev, "tile0", "gt0", "freq0", "cur_freq")); ok {
			s.ClockGPU = intPtr(int(v))
		}
	}
	return s
}

// firstUint 依序讀取 dir 下的各檔案 (dir 為空字串時 names 為完整路徑), 回傳
// 第一個讀得到的值.
func firstUint(dir string, names ...string) (uint64, bool) {
	for _, n := range names {
		p := n
		if dir != "" {
			p = filepath.Join(dir, n)
		}
		if v, ok := readUint(p); ok {
			return v, true
		}
	}
	return 0, false
}

// roundDiv 回傳 v / d 四捨五入的結果, d 須為正數.
func roundDiv(v, d int64) int64 {
	if v < 0 {
		return -((-v + d/2) / d)
	}
	return (v + d/2) / d
}

// dpmLine 符合 amdgpu pp_dpm_sclk 與 pp_dpm_mclk 的一列, 例如 "1: 2500Mhz *".
var dpmLine = regexp.MustCompile(`^\s*\d+:\s*(\d+)\s*[Mm][Hh][Zz]`)

// maxDPMClock 讀取 pp_dpm_sclk 或 pp_dpm_mclk, 回傳所列各級時脈中的最大值
// (MHz); 讀不到時回傳 nil.
func maxDPMClock(path string) *int {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseDPMMax(bufio.NewScanner(f))
}

// parseDPMMax 自 pp_dpm_* 格式的內容找出最大時脈.
func parseDPMMax(sc *bufio.Scanner) *int {
	var best *int
	for sc.Scan() {
		m := dpmLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if best == nil || v > *best {
			best = intPtr(v)
		}
	}
	return best
}
