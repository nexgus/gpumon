package settings

import (
	"encoding/json"
	"strings"
)

// markLegacyHosts 把 JSON 中沒有 identities_only 成員的主機標示為舊版主機. raw 為
// 設定檔的 hosts 成員; 無法逐一對應至 d.Hosts 時不標示.
func markLegacyHosts(d *Data, raw json.RawMessage) {
	if raw == nil {
		return
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || len(items) != len(d.Hosts) {
		return
	}
	for i, item := range items {
		var members map[string]json.RawMessage
		if json.Unmarshal(item, &members) != nil {
			continue
		}
		if _, ok := members["identities_only"]; !ok {
			d.Hosts[i].legacy = true
		}
	}
}

// LegacyDefaults 為升級舊版主機時由 ~/.ssh/config 取得的值. 字串為空與 Port 為 0
// 表示無設定.
type LegacyDefaults struct {
	User           string
	Port           int
	KeyFile        string
	IdentitiesOnly bool
}

// HostUpgrade 為一台舊版主機升級的結果.
type HostUpgrade struct {
	// OldID 為升級前設定檔所用的識別字串 (使用者名稱留空時只有位址, 皆不含連接埠),
	// NewID 為升級後的識別字串.
	OldID string
	NewID string
	// OldRecordingHost 為升級前寫入記錄檔 host 成員的名稱, 即 "使用者名稱@位址",
	// 使用者名稱為升級後的值; 使用者名稱仍空白時為空字串. 升級前以 ~/.ssh/config 的
	// 使用者名稱登入, 故記錄檔的名稱與升級後填入的使用者名稱相同.
	OldRecordingHost string
	// Host 為升級後的表單欄位.
	Host HostConn
}

// HasLegacyHosts 回報是否有待升級的舊版主機.
func (s *Store) HasLegacyHosts() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, h := range s.data.Hosts {
		if h.legacy {
			return true
		}
	}
	return false
}

// UpgradeLegacyHosts 依 ~/.ssh/config 填入每台舊版主機空白的欄位一次: User 空白時
// 填 User, Port 為 0 時填 Port, KeyFile 空白時填第一個 IdentityFile, IdentitiesOnly
// 填 ~/.ssh/config 的值. lookup 以主機的位址為 Host 名稱查詢; 讀不到或沒有對應值時
// 回傳零值, 該欄位維持原狀. 不合法的使用者名稱與連接埠不填入.
//
// 填入後的主機與其他主機重複時, 該主機不填入, 維持原本的欄位. 識別字串改變的
// 主機, 其 GPU 橫列, 程序卡片與最大值卡片的收合狀態, 程序卡片的排序方式與過濾
// 條件, 以及免密碼登入的不再詢問一併改以新的識別字串記錄.
//
// 升級後的主機皆成為新版主機, 結果寫回設定檔. 寫入之前以識別字串改變的主機呼叫
// beforeSave (可為 nil), 供呼叫端搬移記錄檔; 此時持有本物件的鎖, beforeSave 不得
// 呼叫本物件的方法. 寫入失敗時記憶體中的內容仍為升級後的內容, 之後的變更寫入時
// 一併寫出, 並回傳錯誤. 回傳識別字串改變的主機.
func (s *Store) UpgradeLegacyHosts(lookup func(addr string) LegacyDefaults, beforeSave func([]HostUpgrade)) ([]HostUpgrade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.data.clone()
	keys := make([]string, len(c.Hosts))
	for i, h := range c.Hosts {
		keys[i] = h.Key()
	}
	var upgrades []HostUpgrade
	renames := map[string]string{}
	changed := false
	for i := range c.Hosts {
		h := &c.Hosts[i]
		if !h.legacy {
			continue
		}
		changed = true
		h.legacy = false
		oldID := h.legacyID()
		filled := fillLegacy(h.HostConn, lookup(h.Addr))
		if !duplicateKey(keys, i, filled.Key()) {
			h.HostConn = filled
			keys[i] = filled.Key()
		}
		newID := h.ID()
		if newID == oldID {
			continue
		}
		renames[oldID] = newID
		up := HostUpgrade{OldID: oldID, NewID: newID, Host: h.HostConn}
		if h.User != "" {
			up.OldRecordingHost = h.User + "@" + h.Addr
		}
		upgrades = append(upgrades, up)
	}
	if !changed {
		return nil, nil
	}
	renameHostIDs(&c, renames)
	if beforeSave != nil && len(upgrades) > 0 {
		beforeSave(upgrades)
	}
	s.data = c
	return upgrades, save(s.path, c)
}

// fillLegacy 回傳以 def 填入空白欄位後的表單欄位.
func fillLegacy(c HostConn, def LegacyDefaults) HostConn {
	if c.User == "" {
		u := strings.TrimSpace(def.User)
		if u != "" && !strings.ContainsAny(u, "@ \t\r\n") {
			c.User = u
		}
	}
	if c.Port == 0 && def.Port > 0 && def.Port <= 65535 {
		c.Port = def.Port
	}
	if c.KeyFile == "" {
		c.KeyFile = strings.TrimSpace(def.KeyFile)
	}
	c.IdentitiesOnly = def.IdentitiesOnly
	return c
}

func duplicateKey(keys []string, skip int, key string) bool {
	for i, k := range keys {
		if i != skip && k == key {
			return true
		}
	}
	return false
}

// renameHostIDs 把以 renames 的鍵為識別字串的各項狀態改以對應的值記錄. 所有改名
// 同時進行, 一台主機的新識別字串等於另一台主機的舊識別字串時不互相干擾.
func renameHostIDs(d *Data, renames map[string]string) {
	if len(renames) == 0 {
		return
	}
	d.CollapsedGPUs = renameKeys(d.CollapsedGPUs, renames)
	d.ProcSorts = renameKeys(d.ProcSorts, renames)
	d.ProcFilters = renameKeys(d.ProcFilters, renames)
	d.CollapsedProcs = renameList(d.CollapsedProcs, renames)
	d.CollapsedMaxima = renameList(d.CollapsedMaxima, renames)
	d.NoKeySetupPrompt = renameList(d.NoKeySetupPrompt, renames)
}

// renameKeys 回傳鍵依 renames 改名後的新 map. 改名後的鍵與未改名的鍵相同時, 以改名
// 者為準.
func renameKeys[V any](m map[string]V, renames map[string]string) map[string]V {
	out := make(map[string]V, len(m))
	for k, v := range m {
		if _, renamed := renames[k]; !renamed {
			out[k] = v
		}
	}
	for k, v := range m {
		if n, ok := renames[k]; ok {
			out[n] = v
		}
	}
	return out
}

// renameList 回傳依 renames 改名並去除重複後的新切片.
func renameList(list []string, renames map[string]string) []string {
	out := make([]string, len(list))
	for i, x := range list {
		if n, ok := renames[x]; ok {
			x = n
		}
		out[i] = x
	}
	return dedupStrings(out)
}
