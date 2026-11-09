// Package gpu 取得本機 GPU 的靜態資訊與即時取樣, 供遠端程式 gpumon 使用.
//
// NVIDIA 以 NVML 取得, NVML 於執行期以 purego 載入, 找不到即略過; AMD 與
// Intel 讀取 sysfs 與 /proc/<pid>/fdinfo. 不需要 root, 也不變更任何系統設定.
// 本套件只用於 linux 目標, GUI 不引用.
package gpu

import (
	"errors"

	"gpumon/internal/protocol"
)

// Collector 持有所有偵測到的 GPU. Sample 不可同時由多個 goroutine 呼叫.
type Collector struct {
	nvml    *nvmlLib
	nvidia  []*nvidiaGPU
	drm     []*drmGPU
	devices []protocol.Device
	info    *procInfo
	procDir string
	pdevs   map[string]bool
}

// Open 偵測所有支援的 GPU. NVML 不存在時靜默略過 NVIDIA; NVML 存在但初始化
// 失敗時以 logf 記錄後略過. 找不到任何 GPU 不視為錯誤.
func Open(logf func(format string, args ...any)) *Collector {
	return open("/sys", "/proc", logf)
}

func open(sysRoot, procRoot string, logf func(format string, args ...any)) *Collector {
	c := &Collector{info: newProcInfo(procRoot), procDir: procRoot, pdevs: map[string]bool{}}
	lib, err := loadNVML()
	switch {
	case err == nil:
		gpus, devs, err := openNVIDIA(lib, sysRoot, c.info)
		if err != nil {
			logf("%v", err)
		}
		c.nvml, c.nvidia = lib, gpus
		c.devices = append(c.devices, devs...)
	case !errors.Is(err, errNVMLNotFound):
		logf("%v", err)
	}
	gpus, devs := openDRM(sysRoot)
	c.drm = gpus
	c.devices = append(c.devices, devs...)
	for _, g := range gpus {
		c.pdevs[g.pdev] = true
	}
	for i := range c.devices {
		c.devices[i].ID = i
	}
	if c.devices == nil {
		c.devices = []protocol.Device{}
	}
	return c
}

// Devices 回傳所有 GPU 的靜態資訊, ID 由 0 起依序編號, NVIDIA 在前. 沒有 GPU
// 時回傳空 slice.
func (c *Collector) Devices() []protocol.Device {
	return c.devices
}

// Sample 取得所有 GPU 的一筆取樣, 順序與 Devices 相同. withProcs 為 false 時
// 不查詢程序列表, 省去掃描 /proc 的開銷.
func (c *Collector) Sample(withProcs bool) []protocol.GPUSample {
	out := make([]protocol.GPUSample, 0, len(c.devices))
	for _, g := range c.nvidia {
		out = append(out, g.sample(withProcs))
	}
	var drmProcs map[string][]protocol.Process
	if withProcs && len(c.drm) > 0 {
		drmProcs = scanDRMProcs(c.procDir, c.pdevs, c.info)
	}
	for _, g := range c.drm {
		s := g.sample()
		if withProcs {
			s.Procs = drmProcs[g.pdev]
			if s.Procs == nil {
				s.Procs = []protocol.Process{}
			}
		}
		out = append(out, s)
	}
	for i := range out {
		out[i].ID = i
	}
	return out
}

// Close 於有載入 NVML 時呼叫 nvmlShutdown.
func (c *Collector) Close() {
	if c.nvml != nil {
		c.nvml.shutdown()
		c.nvml = nil
	}
}
