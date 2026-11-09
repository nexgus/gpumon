package gpu

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

// 本檔以 purego 於執行期載入 libnvidia-ml.so.1, 不使用 cgo. 函數簽章, 結構與
// 常數依 NVIDIA 官方 nvml.h (NVML_API_VERSION 12) 的定義自行宣告.

// nvmlLibName 為 NVML 共用函式庫的 soname, 由驅動安裝, 不依賴 CUDA toolkit.
const nvmlLibName = "libnvidia-ml.so.1"

// nvmlReturn 對應 nvmlReturn_t.
type nvmlReturn int32

const (
	nvmlSuccess         nvmlReturn = 0
	nvmlErrNotSupported nvmlReturn = 3
	nvmlErrNoPermission nvmlReturn = 4
	nvmlErrNotFound     nvmlReturn = 6
	nvmlErrInsufficient nvmlReturn = 7
	nvmlErrGPUIsLost    nvmlReturn = 15
)

const (
	nvmlTemperatureGPU          = 0
	nvmlClockGraphics           = 0
	nvmlClockMem                = 2
	nvmlDevicePCIBusIDBufSize   = 32
	nvmlDevicePCIBusIDBufV2Size = 16
	nvmlDeviceNameV2BufSize     = 96
	nvmlDeviceUUIDV2BufSize     = 96
)

// nvmlValueNotAvailable 對應 NVML_VALUE_NOT_AVAILABLE, 以 unsigned long long
// 表示時為全 1.
const nvmlValueNotAvailable = ^uint64(0)

// nvmlPciInfo 對應 nvmlPciInfo_t.
type nvmlPciInfo struct {
	BusIDLegacy    [nvmlDevicePCIBusIDBufV2Size]byte
	Domain         uint32
	Bus            uint32
	Device         uint32
	PCIDeviceID    uint32
	PCISubSystemID uint32
	BusID          [nvmlDevicePCIBusIDBufSize]byte
}

// nvmlMemoryV2 對應 nvmlMemory_v2_t.
type nvmlMemoryV2 struct {
	Version  uint32
	Total    uint64
	Reserved uint64
	Free     uint64
	Used     uint64
}

// nvmlMemoryV2Version 對應 nvmlMemory_v2, 即 sizeof(nvmlMemory_v2_t) | (2 << 24).
var nvmlMemoryV2Version = uint32(unsafe.Sizeof(nvmlMemoryV2{})) | 2<<24

// nvmlUtilization 對應 nvmlUtilization_t.
type nvmlUtilization struct {
	GPU    uint32
	Memory uint32
}

// nvmlProcessInfo 對應 nvmlProcessInfo_t (即 nvmlProcessInfo_v2_t).
type nvmlProcessInfo struct {
	PID               uint32
	UsedGPUMemory     uint64
	GPUInstanceID     uint32
	ComputeInstanceID uint32
}

// nvmlProcessUtilizationSample 對應 nvmlProcessUtilizationSample_t.
type nvmlProcessUtilizationSample struct {
	PID       uint32
	TimeStamp uint64
	SMUtil    uint32
	MemUtil   uint32
	EncUtil   uint32
	DecUtil   uint32
}

// nvmlDevice 對應 nvmlDevice_t, 為不透明指標.
type nvmlDevice uintptr

// nvmlLib 為已載入的 NVML 函數. 非必要的函數於函式庫中不存在時為 nil, 對應
// 的欄位視為取不到.
type nvmlLib struct {
	init        func() nvmlReturn
	shutdown    func() nvmlReturn
	errorString func(r nvmlReturn) string
	getCount    func(count *uint32) nvmlReturn
	getHandle   func(index uint32, dev *nvmlDevice) nvmlReturn

	getName          func(dev nvmlDevice, name *byte, length uint32) nvmlReturn
	getUUID          func(dev nvmlDevice, uuid *byte, length uint32) nvmlReturn
	getPciInfo       func(dev nvmlDevice, pci *nvmlPciInfo) nvmlReturn
	getMemoryInfoV2  func(dev nvmlDevice, mem *nvmlMemoryV2) nvmlReturn
	getUtilization   func(dev nvmlDevice, u *nvmlUtilization) nvmlReturn
	getTemperature   func(dev nvmlDevice, sensor uint32, temp *uint32) nvmlReturn
	getPowerUsage    func(dev nvmlDevice, mw *uint32) nvmlReturn
	getEnforcedLimit func(dev nvmlDevice, mw *uint32) nvmlReturn
	getClockInfo     func(dev nvmlDevice, typ uint32, mhz *uint32) nvmlReturn
	getMaxClockInfo  func(dev nvmlDevice, typ uint32, mhz *uint32) nvmlReturn
	getFanSpeed      func(dev nvmlDevice, speed *uint32) nvmlReturn
	getEncoderUtil   func(dev nvmlDevice, util *uint32, periodUs *uint32) nvmlReturn
	getDecoderUtil   func(dev nvmlDevice, util *uint32, periodUs *uint32) nvmlReturn
	getComputeProcs  func(dev nvmlDevice, count *uint32, infos *nvmlProcessInfo) nvmlReturn
	getGraphicsProcs func(dev nvmlDevice, count *uint32, infos *nvmlProcessInfo) nvmlReturn
	getProcessUtil   func(dev nvmlDevice, samples *nvmlProcessUtilizationSample, count *uint32, lastSeen uint64) nvmlReturn
}

// errNVMLNotFound 表示主機上沒有 NVML 函式庫, 即沒有 NVIDIA 驅動.
var errNVMLNotFound = errors.New("nvml: " + nvmlLibName + " not found")

// loadNVML 載入 NVML 並呼叫 nvmlInit_v2. 找不到函式庫時回傳 errNVMLNotFound.
func loadNVML() (*nvmlLib, error) {
	h, err := purego.Dlopen(nvmlLibName, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, errNVMLNotFound
	}
	l := &nvmlLib{}
	required := []struct {
		fptr any
		name string
	}{
		{&l.init, "nvmlInit_v2"},
		{&l.shutdown, "nvmlShutdown"},
		{&l.errorString, "nvmlErrorString"},
		{&l.getCount, "nvmlDeviceGetCount_v2"},
		{&l.getHandle, "nvmlDeviceGetHandleByIndex_v2"},
	}
	for _, f := range required {
		sym, err := purego.Dlsym(h, f.name)
		if err != nil {
			return nil, fmt.Errorf("nvml: missing symbol %s", f.name)
		}
		purego.RegisterFunc(f.fptr, sym)
	}
	optional := []struct {
		fptr any
		name string
	}{
		{&l.getName, "nvmlDeviceGetName"},
		{&l.getUUID, "nvmlDeviceGetUUID"},
		{&l.getPciInfo, "nvmlDeviceGetPciInfo_v3"},
		{&l.getMemoryInfoV2, "nvmlDeviceGetMemoryInfo_v2"},
		{&l.getUtilization, "nvmlDeviceGetUtilizationRates"},
		{&l.getTemperature, "nvmlDeviceGetTemperature"},
		{&l.getPowerUsage, "nvmlDeviceGetPowerUsage"},
		{&l.getEnforcedLimit, "nvmlDeviceGetEnforcedPowerLimit"},
		{&l.getClockInfo, "nvmlDeviceGetClockInfo"},
		{&l.getMaxClockInfo, "nvmlDeviceGetMaxClockInfo"},
		{&l.getFanSpeed, "nvmlDeviceGetFanSpeed"},
		{&l.getEncoderUtil, "nvmlDeviceGetEncoderUtilization"},
		{&l.getDecoderUtil, "nvmlDeviceGetDecoderUtilization"},
		{&l.getComputeProcs, "nvmlDeviceGetComputeRunningProcesses_v3"},
		{&l.getGraphicsProcs, "nvmlDeviceGetGraphicsRunningProcesses_v3"},
		{&l.getProcessUtil, "nvmlDeviceGetProcessUtilization"},
	}
	for _, f := range optional {
		if sym, err := purego.Dlsym(h, f.name); err == nil {
			purego.RegisterFunc(f.fptr, sym)
		}
	}
	if r := l.init(); r != nvmlSuccess {
		return nil, fmt.Errorf("nvml: nvmlInit failed: %s", l.errorString(r))
	}
	return l, nil
}

// cString 取出以 NUL 結尾的 C 字串.
func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
