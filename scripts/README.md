# scripts

本目錄收錄建置用的輔助工具與維護用的腳本.

## licgen/

產生第三方授權清單, 輸出為前端的 JSON 資料檔, 於關於視窗的 "第三方授權" 分頁顯示. 清單涵蓋 Go 的相依套件, 前端的 npm 相依套件, 以及 `licenses/other-materials.json` 所列的其他素材, 產生過程不需網路.

本工具為獨立的 Go module, 由 `build.sh` 於每次建置時呼叫, 不需手動執行.

## syso/

由 `.ico` 產生 Windows 執行檔的資源檔 (.syso), 只寫入圖示資源, 不寫入 application manifest. 建置 Windows 版 GUI 時, Go 把該資源檔連結進執行檔, 作為視窗與檔案總管所顯示的圖示.

本工具為獨立的 Go module, 由 `build.sh` 於建置 Windows 版時呼叫, 不需手動執行.

## reset-settings.sh, reset-settings.bat

清除 GPU Monitor 的所有設定, 使程式下次啟動時以預設值啟動, 等同首次安裝. 記錄檔保留不動.

刪除的對象為資料目錄下的 `settings.json`, 以及程式留下的 `settings.json.*` 備份與暫存檔. 設定包括主機清單, 視窗大小與位置, 外觀, 語言, 曲線, 以及各主機的收合, 排序與過濾狀態. 資料目錄下的 `history/` 存放記錄檔, 不受影響.

| 平台 | 腳本 | 資料目錄 |
|---|---|---|
| macOS | `reset-settings.sh` | `~/Library/Application Support/gpumon/` |
| Windows | `reset-settings.bat` | `%LOCALAPPDATA%\gpumon\` |

環境變數 `GPUMON_DATA_DIR` 有值時, 兩支腳本皆以其為資料目錄, 與 GUI 的判定方式相同.

執行方式如下:

```bash
bash scripts/reset-settings.sh
```

```bat
scripts\reset-settings.bat
```

Windows 上亦可直接雙擊 `reset-settings.bat`, 執行結束時視窗暫停, 待按任意鍵後關閉.

腳本先列出將刪除的檔案, 輸入 `y` 後才刪除; 傳入 `-y` 時不詢問, 直接刪除. GPU Monitor 執行中時腳本拒絕清除, 因程式每次變更設定皆把記憶體中的完整設定寫回檔案, 刪除的設定會被寫回; 先行結束程式再執行腳本即可.

`~/.ssh/known_hosts` 與 OpenSSH 共用, 不屬於 GPU Monitor 的設定, 腳本不修改. WebView 的快取與遠端主機上的 `~/.gpumon/` 亦不影響設定, 腳本不處理.
