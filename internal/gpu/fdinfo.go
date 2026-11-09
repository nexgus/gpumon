package gpu

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gpumon/internal/protocol"
)

// drmClient 為一個 DRM client 於 /proc/<pid>/fdinfo/<fd> 內的用量資訊, 欄位
// 定義見 Linux 核心文件 Documentation/gpu/drm-usage-stats.rst.
type drmClient struct {
	driver   string
	pdev     string
	clientID string
	// engines 為 drm-engine-<引擎> 的累計忙碌時間, 單位為奈秒.
	engines map[string]uint64
	// capacity 為 drm-engine-capacity-<引擎>.
	capacity map[string]uint64
	// cycles 與 totalCycles 為 drm-cycles-<引擎> 與 drm-total-cycles-<引擎>.
	cycles      map[string]uint64
	totalCycles map[string]uint64
	// resident 為 drm-resident-<區域>, legacy 為舊版 amdgpu 的
	// drm-memory-<區域>, 單位皆已換算為位元組.
	resident map[string]uint64
	legacy   map[string]uint64
}

// parseFdinfo 解析一個 fdinfo 檔案的內容; 不是 DRM client (沒有 drm-driver)
// 時 ok 為 false.
func parseFdinfo(b []byte) (c drmClient, ok bool) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		key, val, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		val = strings.TrimSpace(val)
		switch {
		case key == "drm-driver":
			c.driver = val
		case key == "drm-pdev":
			c.pdev = val
		case key == "drm-client-id":
			c.clientID = val
		case strings.HasPrefix(key, "drm-engine-capacity-"):
			putUint(&c.capacity, key[len("drm-engine-capacity-"):], val, "")
		case strings.HasPrefix(key, "drm-engine-"):
			putUint(&c.engines, key[len("drm-engine-"):], val, "ns")
		case strings.HasPrefix(key, "drm-total-cycles-"):
			putUint(&c.totalCycles, key[len("drm-total-cycles-"):], val, "")
		case strings.HasPrefix(key, "drm-cycles-"):
			putUint(&c.cycles, key[len("drm-cycles-"):], val, "")
		case strings.HasPrefix(key, "drm-resident-"):
			putMem(&c.resident, key[len("drm-resident-"):], val)
		case strings.HasPrefix(key, "drm-memory-"):
			putMem(&c.legacy, key[len("drm-memory-"):], val)
		}
	}
	return c, c.driver != ""
}

// putUint 將形如 "123" 或 "123 <unit>" 的值存入 *m[name]; unit 不為空時值必須
// 帶該單位.
func putUint(m *map[string]uint64, name, val, unit string) {
	if name == "" {
		return
	}
	num, u, _ := strings.Cut(val, " ")
	if strings.TrimSpace(u) != unit {
		return
	}
	v, err := strconv.ParseUint(num, 10, 64)
	if err != nil {
		return
	}
	if *m == nil {
		*m = map[string]uint64{}
	}
	(*m)[name] = v
}

// putMem 將形如 "123", "123 KiB" 或 "123 MiB" 的記憶體量換算為位元組後存入
// *m[name].
func putMem(m *map[string]uint64, name, val string) {
	if name == "" {
		return
	}
	num, unit, _ := strings.Cut(val, " ")
	v, err := strconv.ParseUint(num, 10, 64)
	if err != nil {
		return
	}
	switch strings.TrimSpace(unit) {
	case "":
	case "KiB":
		v <<= 10
	case "MiB":
		v <<= 20
	case "GiB":
		v <<= 30
	default:
		return
	}
	if *m == nil {
		*m = map[string]uint64{}
	}
	(*m)[name] = v
}

// memory 回傳此 client 所用的記憶體量. amdgpu 只計 VRAM, 優先取
// drm-resident-vram, 舊核心改取 drm-memory-vram; i915 與 xe 加總所有區域的
// drm-resident-<區域>. 皆沒有時 ok 為 false.
func (c *drmClient) memory() (v uint64, ok bool) {
	if c.driver == "amdgpu" {
		if v, ok := c.resident["vram"]; ok {
			return v, true
		}
		v, ok := c.legacy["vram"]
		return v, ok
	}
	for _, r := range c.resident {
		v += r
	}
	return v, len(c.resident) > 0
}

// addCounters 將 src 各鍵的值加到 *dst.
func addCounters(dst *map[string]uint64, src map[string]uint64) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = map[string]uint64{}
	}
	for k, v := range src {
		(*dst)[k] += v
	}
}

// scanDRMProcs 掃描 procRoot 下所有程序開啟的 DRM 裝置, 依 PCI 位址彙整出
// 各程序的用量. 只處理 pdevs 所列的 PCI 位址. 讀不到其 fd 目錄的程序 (其他
// 帳號的程序) 直接略過, 故非 root 只看得到自己的程序.
//
// 同一個 client 可能因 fork 繼承而出現於多個程序, 以 PCI 位址與
// drm-client-id 去重, 歸給 PID 最小者. 同一程序的多個 client 加總.
func scanDRMProcs(procRoot string, pdevs map[string]bool, info *procInfo) map[string][]protocol.Process {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	slices.Sort(pids)

	type key struct{ pdev, client string }
	seen := map[key]bool{}
	result := map[string][]protocol.Process{}
	for _, pid := range pids {
		dir := filepath.Join(procRoot, strconv.Itoa(pid))
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		perDev := map[string]*protocol.Process{}
		var order []string
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(target, "/dev/dri/") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, "fdinfo", fd.Name()))
			if err != nil {
				continue
			}
			c, ok := parseFdinfo(b)
			if !ok || !pdevs[c.pdev] {
				continue
			}
			if c.clientID != "" {
				k := key{c.pdev, c.clientID}
				if seen[k] {
					continue
				}
				seen[k] = true
			}
			p := perDev[c.pdev]
			if p == nil {
				p = &protocol.Process{PID: pid}
				perDev[c.pdev] = p
				order = append(order, c.pdev)
			}
			addCounters(&p.Engines, c.engines)
			addCounters(&p.Cycles, c.cycles)
			addCounters(&p.TotalCycles, c.totalCycles)
			// capacity 為該類引擎的數量, 各 client 相同, 不加總.
			if len(c.capacity) > 0 && p.EngineCapacity == nil {
				p.EngineCapacity = c.capacity
			}
			if m, ok := c.memory(); ok {
				if p.Mem == nil {
					p.Mem = new(uint64)
				}
				*p.Mem += m
			}
		}
		if len(order) == 0 {
			continue
		}
		user, cmd := info.user(pid), info.cmd(pid)
		for _, pdev := range order {
			p := perDev[pdev]
			p.User, p.Cmd = user, cmd
			result[pdev] = append(result[pdev], *p)
		}
	}
	return result
}
