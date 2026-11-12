package gpu

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gpumon/internal/protocol"
)

// nvidiaGPU 為一張以 NVML 取樣的 NVIDIA GPU.
type nvidiaGPU struct {
	lib  *nvmlLib
	dev  nvmlDevice
	info *procInfo
}

// procUtilWindow 為查詢程序使用率時回溯的時間長度. 驅動程式每 200 ms 記錄一次,
// 每次只記在一個程序名下, 回報的數值為各程序於回溯期間內的平均使用率. 回溯期間
// 過短時, 數值為整張 GPU 的使用率而非該程序所佔的部分, 且未被記錄的程序沒有
// 數值; 實測 5 秒時持續使用 GPU 的程序皆有數值.
const procUtilWindow = 5 * time.Second

// openNVIDIA 列出 NVML 偵測到的所有 GPU. 每張 GPU 的 PCI 代碼依 NVML 回報的
// PCI 位址讀自 sysRoot 下的 bus/pci/devices/<位址>/.
func openNVIDIA(lib *nvmlLib, sysRoot string, info *procInfo) ([]*nvidiaGPU, []protocol.Device, error) {
	var count uint32
	if r := lib.getCount(&count); r != nvmlSuccess {
		return nil, nil, fmt.Errorf("nvml: nvmlDeviceGetCount failed: %s", lib.errorString(r))
	}
	var gpus []*nvidiaGPU
	var devs []protocol.Device
	for i := range count {
		var h nvmlDevice
		if r := lib.getHandle(i, &h); r != nvmlSuccess {
			// 單張 GPU 取不到 handle (例如已脫離匯流排) 時略過該張, 其餘照常.
			continue
		}
		g := &nvidiaGPU{lib: lib, dev: h, info: info}
		gpus = append(gpus, g)
		devs = append(devs, g.device(sysRoot))
	}
	return gpus, devs, nil
}

// device 取得此 GPU 的靜態資訊, ID 由呼叫端填入.
func (g *nvidiaGPU) device(sysRoot string) protocol.Device {
	l := g.lib
	d := protocol.Device{Vendor: protocol.VendorNVIDIA}
	if l.getName != nil {
		buf := make([]byte, nvmlDeviceNameV2BufSize)
		if l.getName(g.dev, &buf[0], uint32(len(buf))) == nvmlSuccess {
			d.Name = cString(buf)
		}
	}
	if d.Name == "" {
		d.Name = "NVIDIA GPU"
	}
	if l.getUUID != nil {
		buf := make([]byte, nvmlDeviceUUIDV2BufSize)
		if l.getUUID(g.dev, &buf[0], uint32(len(buf))) == nvmlSuccess {
			d.UUID = cString(buf)
		}
	}
	if m, ok := g.memory(); ok {
		d.MemTotal = &m.Total
	}
	if l.getEnforcedLimit != nil {
		var mw uint32
		if l.getEnforcedLimit(g.dev, &mw) == nvmlSuccess {
			d.PowerMaxW = intPtr(milliToUnit(mw))
		}
	}
	if l.getMaxClockInfo != nil {
		var mhz uint32
		if l.getMaxClockInfo(g.dev, nvmlClockGraphics, &mhz) == nvmlSuccess {
			d.ClockGPUMax = intPtr(int(mhz))
		}
		if l.getMaxClockInfo(g.dev, nvmlClockMem, &mhz) == nvmlSuccess {
			d.ClockMemMax = intPtr(int(mhz))
		}
	}
	if l.getPciInfo != nil {
		var pci nvmlPciInfo
		if l.getPciInfo(g.dev, &pci) == nvmlSuccess {
			if addr := sysfsPCIAddr(cString(pci.BusID[:])); addr != "" {
				codes := readPCICodes(filepath.Join(sysRoot, "bus", "pci", "devices", addr))
				d.PCIVendor, d.PCIDevice = codes.vendor, codes.device
				d.PCISubsysVendor, d.PCISubsysDevice = codes.subsysVendor, codes.subsysDevice
			}
		}
	}
	return d
}

// sysfsPCIAddr 將 NVML 的 PCI 位址 (例如 "00000000:01:00.0", 網域為 8 位
// 十六進位數字) 轉為 sysfs 使用的形式 (例如 "0000:01:00.0", 網域為 4 位小寫
// 十六進位數字); 格式不符時回傳空字串.
func sysfsPCIAddr(busID string) string {
	domain, rest, ok := strings.Cut(strings.ToLower(busID), ":")
	if !ok || rest == "" {
		return ""
	}
	d, err := strconv.ParseUint(domain, 16, 32)
	if err != nil || d > 0xffff {
		return ""
	}
	return fmt.Sprintf("%04x:%s", d, rest)
}

// memory 以 nvmlDeviceGetMemoryInfo_v2 取得記憶體資訊, 其 used 不含驅動保留
// 的部分, 與 nvidia-smi 的 memory.used 一致.
func (g *nvidiaGPU) memory() (nvmlMemoryV2, bool) {
	if g.lib.getMemoryInfoV2 == nil {
		return nvmlMemoryV2{}, false
	}
	m := nvmlMemoryV2{Version: nvmlMemoryV2Version}
	return m, g.lib.getMemoryInfoV2(g.dev, &m) == nvmlSuccess
}

// sample 取得此 GPU 的一筆取樣. withProcs 為 false 時不查詢程序列表.
func (g *nvidiaGPU) sample(withProcs bool) protocol.GPUSample {
	l := g.lib
	var s protocol.GPUSample
	var v uint32
	if l.getUtilization != nil {
		var u nvmlUtilization
		if l.getUtilization(g.dev, &u) == nvmlSuccess {
			s.Util = intPtr(int(u.GPU))
		}
	}
	if m, ok := g.memory(); ok {
		s.MemUsed = &m.Used
	}
	if l.getTemperature != nil && l.getTemperature(g.dev, nvmlTemperatureGPU, &v) == nvmlSuccess {
		s.Temp = intPtr(int(v))
	}
	if l.getPowerUsage != nil && l.getPowerUsage(g.dev, &v) == nvmlSuccess {
		s.PowerW = intPtr(milliToUnit(v))
	}
	if l.getClockInfo != nil {
		if l.getClockInfo(g.dev, nvmlClockGraphics, &v) == nvmlSuccess {
			s.ClockGPU = intPtr(int(v))
		}
		if l.getClockInfo(g.dev, nvmlClockMem, &v) == nvmlSuccess {
			s.ClockMem = intPtr(int(v))
		}
	}
	if l.getFanSpeed != nil && l.getFanSpeed(g.dev, &v) == nvmlSuccess {
		s.Fan = intPtr(int(v))
	}
	var period uint32
	if l.getEncoderUtil != nil && l.getEncoderUtil(g.dev, &v, &period) == nvmlSuccess {
		s.Enc = intPtr(int(v))
	}
	if l.getDecoderUtil != nil && l.getDecoderUtil(g.dev, &v, &period) == nvmlSuccess {
		s.Dec = intPtr(int(v))
	}
	if withProcs {
		s.Procs = g.procs()
	}
	return s
}

// procs 合併運算與繪圖兩份程序列表, 並附上各程序的使用率. 兩份列表皆取不到
// 時回傳 nil.
func (g *nvidiaGPU) procs() []protocol.Process {
	compute, okC := g.runningProcs(g.lib.getComputeProcs)
	graphics, okG := g.runningProcs(g.lib.getGraphicsProcs)
	if !okC && !okG {
		return nil
	}
	byPID := map[uint32]*protocol.Process{}
	var pids []uint32
	for _, list := range [][]nvmlProcessInfo{compute, graphics} {
		for _, pi := range list {
			p := byPID[pi.PID]
			if p == nil {
				p = &protocol.Process{PID: int(pi.PID)}
				byPID[pi.PID] = p
				pids = append(pids, pi.PID)
			}
			// 同一程序同時出現在兩份列表時, 兩者回報的是同一份記憶體, 取較大者.
			if pi.UsedGPUMemory != nvmlValueNotAvailable && (p.Mem == nil || pi.UsedGPUMemory > *p.Mem) {
				m := pi.UsedGPUMemory
				p.Mem = &m
			}
		}
	}
	utils := g.processUtil()
	slices.Sort(pids)
	out := make([]protocol.Process, 0, len(pids))
	for _, pid := range pids {
		p := byPID[pid]
		if u, ok := utils[pid]; ok {
			p.Util = intPtr(u)
		}
		p.User = g.info.user(p.PID)
		p.Cmd = g.info.cmd(p.PID)
		out = append(out, *p)
	}
	return out
}

// runningProcs 呼叫 nvmlDeviceGet{Compute,Graphics}RunningProcesses_v3, 緩衝區
// 不足時依回報的數量加大後重試.
func (g *nvidiaGPU) runningProcs(fn func(nvmlDevice, *uint32, *nvmlProcessInfo) nvmlReturn) ([]nvmlProcessInfo, bool) {
	if fn == nil {
		return nil, false
	}
	buf := make([]nvmlProcessInfo, 16)
	for range 4 {
		n := uint32(len(buf))
		r := fn(g.dev, &n, &buf[0])
		switch r {
		case nvmlSuccess:
			return buf[:n], true
		case nvmlErrInsufficient:
			buf = make([]nvmlProcessInfo, n+16)
		default:
			return nil, false
		}
	}
	return nil, false
}

// processUtil 回傳各程序於最近 procUtilWindow 內的平均 SM 使用率, 同一程序有
// 多筆取樣時取最新一筆. 此期間內沒有使用 GPU 的程序不在回傳的內容中.
func (g *nvidiaGPU) processUtil() map[uint32]int {
	fn := g.lib.getProcessUtil
	if fn == nil {
		return nil
	}
	last := uint64(time.Now().Add(-procUtilWindow).UnixMicro())

	var n uint32
	r := fn(g.dev, nil, &n, last)
	if r != nvmlErrInsufficient || n == 0 {
		return nil
	}
	buf := make([]nvmlProcessUtilizationSample, n+8)
	n = uint32(len(buf))
	if fn(g.dev, &buf[0], &n, last) != nvmlSuccess {
		return nil
	}
	latest := map[uint32]nvmlProcessUtilizationSample{}
	for _, s := range buf[:n] {
		if cur, ok := latest[s.PID]; !ok || s.TimeStamp > cur.TimeStamp {
			latest[s.PID] = s
		}
	}
	out := make(map[uint32]int, len(latest))
	for pid, s := range latest {
		out[pid] = int(s.SMUtil)
	}
	return out
}

// milliToUnit 將毫瓦換算為瓦, 四捨五入.
func milliToUnit(v uint32) int {
	return int((uint64(v) + 500) / 1000)
}

func intPtr(v int) *int { return &v }
