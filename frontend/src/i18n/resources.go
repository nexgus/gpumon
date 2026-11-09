// Package i18n 讓 Go 端讀取前端的語言資源檔 (en.json 與 zh-TW.json), 使主選單與
// 匣圖示選單的文字與前端共用同一份資源, 不另行維護.
package i18n

import (
	"embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed en.json zh-TW.json
var files embed.FS

// Fallback 為所選語言的資源檔缺少某個鍵時改用的語言.
const Fallback = "en"

var (
	loadOnce  sync.Once
	resources map[string]map[string]string
)

func load() {
	resources = map[string]map[string]string{}
	entries, err := files.ReadDir(".")
	if err != nil {
		return
	}
	for _, e := range entries {
		lang, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		raw, err := files.ReadFile(e.Name())
		if err != nil {
			continue
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		resources[lang] = m
	}
}

// Text 回傳 key 於 lang 的文字. lang 的資源檔缺少該鍵時改用英文, 英文亦缺少時回傳
// key 本身, 與前端 i18n/index.ts 的 translate 相同.
func Text(lang, key string) string {
	loadOnce.Do(load)
	if s, ok := resources[lang][key]; ok {
		return s
	}
	if s, ok := resources[Fallback][key]; ok {
		return s
	}
	return key
}

// Has 回報 lang 的資源檔是否含有 key, 供測試確認選單所用的鍵皆存在.
func Has(lang, key string) bool {
	loadOnce.Do(load)
	_, ok := resources[lang][key]
	return ok
}
