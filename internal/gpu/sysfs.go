package gpu

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// readTrim 讀取一個 sysfs 檔案並去除前後空白.
func readTrim(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// readInt 讀取內容為一個十進位整數的檔案.
func readInt(path string) (int64, bool) {
	s, err := readTrim(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// readUint 讀取內容為一個非負十進位整數的檔案.
func readUint(path string) (uint64, bool) {
	s, err := readTrim(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// pciCodes 為 PCI 裝置目錄下四個代碼檔案的內容.
type pciCodes struct {
	vendor, device, subsysVendor, subsysDevice string
}

// readPCICodes 讀取 PCI 裝置目錄 dir 下的 vendor, device, subsystem_vendor,
// subsystem_device, 轉為 4 位小寫十六進位數字; 讀不到者為空字串.
func readPCICodes(dir string) pciCodes {
	return pciCodes{
		vendor:       readPCIHex(filepath.Join(dir, "vendor")),
		device:       readPCIHex(filepath.Join(dir, "device")),
		subsysVendor: readPCIHex(filepath.Join(dir, "subsystem_vendor")),
		subsysDevice: readPCIHex(filepath.Join(dir, "subsystem_device")),
	}
}

// readPCIHex 讀取內容形如 0x10de 的檔案, 回傳 "10de"; 格式不符時回傳空字串.
func readPCIHex(path string) string {
	s, err := readTrim(path)
	if err != nil {
		return ""
	}
	return normalizePCIHex(s)
}

// normalizePCIHex 將 0x10DE 之類的字串正規化為 4 位小寫十六進位數字.
func normalizePCIHex(s string) string {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	v, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return ""
	}
	return strconv.FormatUint(v|0x10000, 16)[1:]
}

// findHwmon 回傳 PCI 裝置目錄 dir 下第一個 hwmon 目錄; 沒有時回傳空字串.
func findHwmon(dir string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "hwmon", "hwmon*"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// pciIDsPaths 為各發行版放置 pci.ids 的位置, 依序嘗試.
var pciIDsPaths = []string{
	"/usr/share/hwdata/pci.ids",
	"/usr/share/misc/pci.ids",
	"/usr/share/pci.ids",
}

// lookupPCIName 自 pci.ids 查出廠商與產品代碼對應的產品名稱, 例如
// "Raptor Lake-S GT1 [UHD Graphics 770]"; 查不到時回傳空字串. vendor 與
// device 為 4 位小寫十六進位數字.
func lookupPCIName(vendor, device string) string {
	if vendor == "" || device == "" {
		return ""
	}
	for _, p := range pciIDsPaths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		name := scanPCIIDs(bufio.NewScanner(f), vendor, device)
		f.Close()
		if name != "" {
			return name
		}
	}
	return ""
}

// scanPCIIDs 於 pci.ids 格式的內容中找出 vendor 底下 device 的名稱.
// 廠商列不縮排, 產品列以一個 tab 縮排, 子系統列以兩個 tab 縮排.
func scanPCIIDs(sc *bufio.Scanner, vendor, device string) string {
	inVendor := false
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] != '\t' {
			if inVendor {
				return ""
			}
			inVendor = strings.HasPrefix(line, vendor+"  ")
			continue
		}
		if !inVendor || strings.HasPrefix(line, "\t\t") {
			continue
		}
		rest := line[1:]
		if strings.HasPrefix(rest, device+"  ") {
			return strings.TrimSpace(rest[len(device)+2:])
		}
	}
	return ""
}
