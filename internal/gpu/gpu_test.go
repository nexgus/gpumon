package gpu

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gpumon/internal/protocol"
)

// writeFile 於測試目錄建立檔案, 必要時建立上層目錄.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

const i915Fdinfo = `pos:	0
flags:	0100002
mnt_id:	26
drm-driver:	i915
drm-client-id:	18
drm-pdev:	0000:00:02.0
drm-total-system0:	8 MiB
drm-resident-system0:	4 MiB
drm-resident-stolen-system0:	64 KiB
drm-engine-render:	12345 ns
drm-engine-copy:	0 ns
drm-engine-video:	77 ns
drm-engine-capacity-video:	2
drm-engine-video-enhance:	5 ns
`

const xeFdinfo = `drm-driver:	xe
drm-client-id:	4
drm-pdev:	0000:03:00.0
drm-total-gtt:	4 KiB
drm-resident-vram0:	1024 KiB
drm-cycles-rcs:	28257900
drm-total-cycles-rcs:	7655183225
drm-engine-capacity-vcs:	2
`

const amdFdinfo = `drm-driver:	amdgpu
drm-pdev:	0000:0a:00.0
drm-client-id:	60
drm-memory-vram:	2048 KiB
drm-memory-gtt:	512 KiB
drm-engine-gfx:	1234 ns
drm-engine-compute:	0 ns
`

func TestParseFdinfoI915(t *testing.T) {
	c, ok := parseFdinfo([]byte(i915Fdinfo))
	if !ok || c.driver != "i915" || c.pdev != "0000:00:02.0" || c.clientID != "18" {
		t.Fatalf("header mismatch: %+v", c)
	}
	if c.engines["render"] != 12345 || c.engines["video"] != 77 || c.engines["video-enhance"] != 5 || len(c.engines) != 4 {
		t.Errorf("engines = %v", c.engines)
	}
	if c.capacity["video"] != 2 || len(c.capacity) != 1 {
		t.Errorf("capacity = %v", c.capacity)
	}
	if m, ok := c.memory(); !ok || m != 4<<20+64<<10 {
		t.Errorf("memory = %d, %v", m, ok)
	}
}

func TestParseFdinfoXe(t *testing.T) {
	c, ok := parseFdinfo([]byte(xeFdinfo))
	if !ok || c.driver != "xe" {
		t.Fatalf("not parsed: %+v", c)
	}
	if c.cycles["rcs"] != 28257900 || c.totalCycles["rcs"] != 7655183225 {
		t.Errorf("cycles = %v, total = %v", c.cycles, c.totalCycles)
	}
	if len(c.engines) != 0 {
		t.Errorf("engines should be empty, got %v", c.engines)
	}
	if m, ok := c.memory(); !ok || m != 1<<20 {
		t.Errorf("memory = %d, %v", m, ok)
	}
}

func TestParseFdinfoAMD(t *testing.T) {
	c, ok := parseFdinfo([]byte(amdFdinfo))
	if !ok || c.engines["gfx"] != 1234 {
		t.Fatalf("parsed %+v", c)
	}
	if m, ok := c.memory(); !ok || m != 2<<20 {
		t.Errorf("memory = %d, %v (want VRAM only)", m, ok)
	}
	c, _ = parseFdinfo([]byte(amdFdinfo + "drm-resident-vram:\t1024 KiB\n"))
	if m, _ := c.memory(); m != 1<<20 {
		t.Errorf("memory = %d, want drm-resident-vram preferred", m)
	}
}

func TestParseFdinfoNotDRM(t *testing.T) {
	if _, ok := parseFdinfo([]byte("pos:\t0\nflags:\t02\n")); ok {
		t.Error("non-DRM fdinfo parsed as DRM")
	}
}

func TestScanDRMProcs(t *testing.T) {
	root := t.TempDir()
	// PID 100 開啟兩個 client, 其中 client 18 亦被 PID 200 繼承.
	symlink(t, "/dev/dri/renderD128", filepath.Join(root, "100", "fd", "3"))
	writeFile(t, filepath.Join(root, "100", "fdinfo", "3"), i915Fdinfo)
	symlink(t, "/dev/dri/card1", filepath.Join(root, "100", "fd", "4"))
	writeFile(t, filepath.Join(root, "100", "fdinfo", "4"), strings.Replace(i915Fdinfo, "drm-client-id:\t18", "drm-client-id:\t19", 1))
	symlink(t, "/dev/null", filepath.Join(root, "100", "fd", "0"))
	writeFile(t, filepath.Join(root, "100", "fdinfo", "0"), "pos:\t0\n")
	writeFile(t, filepath.Join(root, "100", "status"), "Name:\tglxgears\nUid:\t0\t0\t0\t0\n")
	writeFile(t, filepath.Join(root, "100", "cmdline"), "glxgears\x00-info\x00")

	symlink(t, "/dev/dri/renderD128", filepath.Join(root, "200", "fd", "5"))
	writeFile(t, filepath.Join(root, "200", "fdinfo", "5"), i915Fdinfo)
	// PID 300 使用不在 pdevs 內的裝置.
	symlink(t, "/dev/dri/renderD129", filepath.Join(root, "300", "fd", "3"))
	writeFile(t, filepath.Join(root, "300", "fdinfo", "3"), amdFdinfo)
	writeFile(t, filepath.Join(root, "self"), "")

	got := scanDRMProcs(root, map[string]bool{"0000:00:02.0": true}, newProcInfo(root))
	procs := got["0000:00:02.0"]
	if len(got) != 1 || len(procs) != 1 {
		t.Fatalf("got %+v", got)
	}
	p := procs[0]
	if p.PID != 100 || p.Cmd != "glxgears -info" || p.User != "root" {
		t.Errorf("process = %+v", p)
	}
	if p.Engines["render"] != 2*12345 {
		t.Errorf("render = %d, want sum of two clients", p.Engines["render"])
	}
	if p.EngineCapacity["video"] != 2 {
		t.Errorf("capacity = %v", p.EngineCapacity)
	}
	if p.Mem == nil || *p.Mem != 2*(4<<20+64<<10) {
		t.Errorf("mem = %v", p.Mem)
	}
}

// makeCard 建立一張假的 DRM card 與其 PCI 裝置目錄.
func makeCard(t *testing.T, sys, card, pdev, driver, vendor, device string) string {
	t.Helper()
	dev := filepath.Join(sys, "devices", "pci0000:00", pdev)
	writeFile(t, filepath.Join(dev, "vendor"), "0x"+vendor+"\n")
	writeFile(t, filepath.Join(dev, "device"), "0x"+device+"\n")
	writeFile(t, filepath.Join(dev, "subsystem_vendor"), "0x15D9\n")
	writeFile(t, filepath.Join(dev, "subsystem_device"), "0x1c9e\n")
	symlink(t, filepath.Join(sys, "bus", "pci", "drivers", driver), filepath.Join(dev, "driver"))
	symlink(t, dev, filepath.Join(sys, "class", "drm", card, "device"))
	return dev
}

func TestOpenDRMAndSample(t *testing.T) {
	sys := t.TempDir()
	makeCard(t, sys, "card0", "0000:01:00.0", "nvidia", "10de", "2bb1")
	intel := makeCard(t, sys, "card1", "0000:00:02.0", "i915", "8086", "a780")
	amd := makeCard(t, sys, "card2", "0000:0a:00.0", "amdgpu", "1002", "744c")
	symlink(t, filepath.Join(sys, "class", "drm", "card1"), filepath.Join(sys, "class", "drm", "card1-DP-1"))
	card1 := filepath.Join(sys, "class", "drm", "card1")
	writeFile(t, filepath.Join(card1, "gt", "gt0", "rps_RP0_freq_mhz"), "1650\n")
	writeFile(t, filepath.Join(card1, "gt", "gt0", "rps_cur_freq_mhz"), "300\n")
	_ = intel

	writeFile(t, filepath.Join(amd, "product_name"), "Radeon RX 7900 XTX\n")
	writeFile(t, filepath.Join(amd, "mem_info_vram_total"), "25753026560\n")
	writeFile(t, filepath.Join(amd, "mem_info_vram_used"), "1048576\n")
	writeFile(t, filepath.Join(amd, "gpu_busy_percent"), "42\n")
	writeFile(t, filepath.Join(amd, "pp_dpm_sclk"), "0: 500Mhz\n1: 1800Mhz *\n2: 2500Mhz\n")
	writeFile(t, filepath.Join(amd, "pp_dpm_mclk"), "0: 96Mhz\n1: 1250Mhz *\n")
	hw := filepath.Join(amd, "hwmon", "hwmon3")
	writeFile(t, filepath.Join(hw, "temp1_input"), "45500\n")
	writeFile(t, filepath.Join(hw, "power1_average"), "123400000\n")
	writeFile(t, filepath.Join(hw, "power1_cap"), "339000000\n")
	writeFile(t, filepath.Join(hw, "freq1_input"), "1800000000\n")
	writeFile(t, filepath.Join(hw, "freq2_input"), "1250000000\n")
	writeFile(t, filepath.Join(hw, "pwm1"), "128\n")

	gpus, devs := openDRM(sys)
	if len(gpus) != 2 || len(devs) != 2 {
		t.Fatalf("got %d gpus: %+v", len(gpus), devs)
	}
	d := devs[0]
	if d.Vendor != protocol.VendorIntel || d.PCIVendor != "8086" || d.PCIDevice != "a780" ||
		d.PCISubsysVendor != "15d9" || d.PCISubsysDevice != "1c9e" {
		t.Errorf("intel device = %+v", d)
	}
	if d.ClockGPUMax == nil || *d.ClockGPUMax != 1650 || d.MemTotal != nil || d.PowerMaxW != nil {
		t.Errorf("intel limits = %+v", d)
	}
	if gpus[0].pdev != "0000:00:02.0" {
		t.Errorf("pdev = %s", gpus[0].pdev)
	}
	s := gpus[0].sample()
	if s.ClockGPU == nil || *s.ClockGPU != 300 || s.Util != nil || s.Temp != nil {
		t.Errorf("intel sample = %+v", s)
	}

	d = devs[1]
	if d.Vendor != protocol.VendorAMD || d.Name != "Radeon RX 7900 XTX" || *d.MemTotal != 25753026560 ||
		*d.PowerMaxW != 339 || *d.ClockGPUMax != 2500 || *d.ClockMemMax != 1250 {
		t.Errorf("amd device = %+v", d)
	}
	s = gpus[1].sample()
	if *s.Util != 42 || *s.MemUsed != 1048576 || *s.Temp != 46 || *s.PowerW != 123 ||
		*s.ClockGPU != 1800 || *s.ClockMem != 1250 || *s.Fan != 50 || s.Enc != nil {
		t.Errorf("amd sample = %+v", s)
	}
}

func TestNormalizePCIHex(t *testing.T) {
	cases := map[string]string{"0x10de": "10de", "0x8086": "8086", "0x15D9": "15d9", "0x0001": "0001", "bogus": "", "0x123456": ""}
	for in, want := range cases {
		if got := normalizePCIHex(in); got != want {
			t.Errorf("normalizePCIHex(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSysfsPCIAddr(t *testing.T) {
	cases := map[string]string{
		"00000000:01:00.0": "0000:01:00.0",
		"00000001:A1:00.0": "0001:a1:00.0",
		"0000:02:00.0":     "0000:02:00.0",
		"bogus":            "",
	}
	for in, want := range cases {
		if got := sysfsPCIAddr(in); got != want {
			t.Errorf("sysfsPCIAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScanPCIIDs(t *testing.T) {
	const ids = `# comment
10de  NVIDIA Corporation
	2bb1  GB202GL [RTX PRO 6000 Blackwell Workstation Edition]
		103c 204b  Subsystem name
8086  Intel Corporation
	a780  Raptor Lake-S GT1 [UHD Graphics 770]
		15d9 1c9e  Subsystem name
	a781  Other
C 03  Display controller
`
	scan := func(v, d string) string {
		return scanPCIIDs(bufio.NewScanner(strings.NewReader(ids)), v, d)
	}
	if got := scan("8086", "a780"); got != "Raptor Lake-S GT1 [UHD Graphics 770]" {
		t.Errorf("got %q", got)
	}
	if got := scan("10de", "a780"); got != "" {
		t.Errorf("device from another vendor matched: %q", got)
	}
	if got := scan("8086", "1c9e"); got != "" {
		t.Errorf("subsystem line matched: %q", got)
	}
}

func TestFormatCmdline(t *testing.T) {
	if got := formatCmdline([]byte("python\x00train.py\x00--lr\x000.1\x00")); got != "python train.py --lr 0.1" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("a", maxCmdLen+100)
	if got := formatCmdline([]byte(long)); len(got) != maxCmdLen {
		t.Errorf("len = %d, want %d", len(got), maxCmdLen)
	}
}

func TestProcInfoCmdFallback(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "5", "cmdline"), "")
	writeFile(t, filepath.Join(root, "5", "comm"), "kworker\n")
	writeFile(t, filepath.Join(root, "5", "status"), "Uid:\t4242424\t0\t0\t0\n")
	p := newProcInfo(root)
	if got := p.cmd(5); got != "[kworker]" {
		t.Errorf("cmd = %q", got)
	}
	if got := p.user(5); got != "4242424" {
		t.Errorf("user = %q, want numeric uid fallback", got)
	}
	if got := p.user(6); got != "" {
		t.Errorf("user of missing pid = %q", got)
	}
}

func TestParseDPMMax(t *testing.T) {
	got := parseDPMMax(bufio.NewScanner(strings.NewReader("0: 96Mhz\n1: 456Mhz\n2: 1000Mhz *\n")))
	if got == nil || *got != 1000 {
		t.Errorf("got %v", got)
	}
	if parseDPMMax(bufio.NewScanner(strings.NewReader("garbage\n"))) != nil {
		t.Error("expected nil")
	}
}
