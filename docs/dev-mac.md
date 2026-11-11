# 建立開發環境 (macOS)

開發環境需要一台搭載 Apple 晶片 (arm64) 的 Mac.

## 1. 安裝 Xcode Command Line Tools

Xcode Command Line Tools 提供下載原始碼用的 `git`, 以及建置 macOS 版 GUI 所需的 C 編譯器.

先確認是否已經安裝:

```sh
xcode-select -p
```

畫面顯示 `/Library/Developer/CommandLineTools` 或 `/Applications/Xcode.app/Contents/Developer` 時, 表示已經安裝, 請直接進行第 2 節. 畫面顯示其他訊息時, 以下列指令安裝, 並依跳出的對話框完成安裝:

```sh
xcode-select --install
```

## 2. 下載原始碼

```sh
cd ~
git clone https://github.com/nexgus/gpumon.git
```

## 3. 安裝建置工具

### 3.1 安裝 Go

Go 以官方安裝檔安裝. 在 Go 的官方下載頁 <https://go.dev/dl/> 上方的 "Featured downloads" 區塊, 下載標示 "Apple macOS (ARM64)" 的安裝檔, 檔名為 `go<版本>.darwin-arm64.pkg`. 執行安裝檔並依指示完成安裝, 安裝完成後重新開啟終端機.

### 3.2 安裝 Node.js

Node.js 以 nvm 安裝. nvm 的安裝程式會把設定寫入 `~/.zshrc`, 但全新的 macOS 沒有這個檔案, 因此先建立該檔案.

```sh
touch ~/.zshrc
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.41.0/install.sh | bash
```

安裝完成後重新開啟終端機, 再安裝目前的長期支援 (LTS) 版 Node.js:

```sh
nvm install --lts
```

### 3.3 安裝 Homebrew

Windows 安裝檔 `.msi` 由 msitools 中的 `wixl` 產生. macOS 沒有內建 msitools, 第一次打包安裝包時, 建置腳本會以 Homebrew 自動安裝 msitools.

`brew --version` 顯示 `Homebrew` 開頭的版本號碼時, 表示已經安裝, 請直接進行第 3.4 節. 尚未安裝時, 以下列指令安裝:

```sh
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

安裝完成後, 畫面最後會出現 `Next steps:` 段落, 其中列出三道指令. 這三道指令把 Homebrew 加入終端機的搜尋路徑 (PATH), 必須執行, 否則終端機找不到 `brew` 指令. 以 macOS 預設的 zsh 而言, 這三道指令如下:

```sh
echo >> ~/.zprofile
echo 'eval "$(/opt/homebrew/bin/brew shellenv zsh)"' >> ~/.zprofile
eval "$(/opt/homebrew/bin/brew shellenv zsh)"
```

畫面上顯示的指令與上方不同時, 以畫面上顯示的指令為準.

### 3.4 確認版本

`go version` 須顯示 1.26 以上, 此為 `go.mod` 的要求. `node --version` 須顯示 18 版或 20 以上, 此為前端建置工具 Vite 的要求.

## 4. 設定 graphify (選用)

graphify 把原始碼整理成知識圖譜 (knowledge graph), 讓 Claude Code 查詢程式結構時不必逐一讀取原始碼檔案.

### 4.1 安裝 graphify

graphify 以 uv 安裝. 先安裝 uv:

```sh
curl -LsSf https://astral.sh/uv/install.sh | sh
```

安裝完成後重新開啟終端機, 再安裝 graphify:

```sh
uv tool install graphifyy
uv tool update-shell
```

套件名稱 `graphifyy` 結尾有兩個 y, 安裝後的指令名稱為 `graphify`. 執行完畢後重新開啟終端機.

### 4.2 將 graphify 設定至本專案

```sh
cd ~/gpumon
graphify claude install
```

這道指令會在原始碼目錄內產生兩項設定:

- 在 `CLAUDE.md` 寫入 graphify 的使用規則.
- 在 `.claude/settings.json` 登錄 hook, 使 Claude Code 搜尋或讀取原始碼前先查詢知識圖譜.

`CLAUDE.md` 與 `.claude/` 皆列於 `.gitignore`, 不隨原始碼下載. 因此每位開發者下載原始碼後, 都必須自行執行一次這道指令.

### 4.3 建立知識圖譜

```sh
cd ~/gpumon
graphify update .
```

這道指令只分析原始碼結構, 不需要任何 API key. 產出位於 `graphify-out/` 目錄, 這個目錄同樣列於 `.gitignore`. 日後修改原始碼後, 再執行一次同一道指令即可更新知識圖譜.

## 5. 建置

開發期間建置執行檔:

```sh
cd ~/gpumon
bash build.sh
```

建置執行檔並打包安裝包:

```sh
cd ~/gpumon
bash build.sh --all
```

建置過程中, npm 可能顯示下列兩類訊息. 這兩類訊息不影響建置, 不需處理:

- 提示有套件存在安全性弱點 (`vulnerabilities`), 並建議執行 `npm audit fix --force`. 請勿執行該指令, 該指令會變更本專案鎖定的套件版本.
- 以 `npm warn install-scripts` 開頭的數行警告.

產出皆位於 `bin/` 目錄, 檔名中的 `<版本>` 由版本號碼與 commit 代碼組成, 例如 `0.1.0+5edbe3e`. 工作目錄有尚未 commit 的修改時, 版本後方會再加上 `-dirty`.

| 檔案 | 用途 | 產生的指令 |
|---|---|---|
| `gpumon-<版本>-linux-amd64.bin` | 遠端程式. 這個程式已內嵌於 GUI, 不需另行處理. | 兩者皆產生 |
| `gpumon-<版本>-darwin-arm64` | macOS 版 GUI. | 兩者皆產生 |
| `gpumon-<版本>-windows-amd64.exe` | Windows 版 GUI. | 兩者皆產生 |
| `gpumon` | 指向本次建置的 macOS 版 GUI 的符號連結 (symbolic link). | 兩者皆產生 |
| `gpumon-<版本>-darwin-arm64.app` | macOS 的應用程式. | 僅 `bash build.sh --all` |
| `gpumon-<版本>-darwin-arm64.pkg` | macOS 的安裝檔, 把應用程式安裝至 "應用程式" 資料夾. | 僅 `bash build.sh --all` |
| `gpumon-<版本>-windows-amd64.msi` | Windows 的安裝檔. | 僅 `bash build.sh --all` |

再次建置時, 腳本會先把 `bin/` 內的舊產出依版本搬到 `bin/archive/<版本>/`, 再產生新的產出.
