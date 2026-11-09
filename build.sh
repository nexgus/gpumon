#!/bin/bash
# 建置 gpumon 的遠端程式與本機 GUI "GPU Monitor", 傳入 --all 時另行打包安裝包.
# 全部於 macOS 上建置, 可用 bash 或 zsh 執行.
#
# 用法: bash build.sh [-a|--all]
#   不傳入選項時建置三個執行檔: 遠端程式 (linux/amd64), GUI (darwin/arm64) 與
#   GUI (windows/amd64).
#   -a, --all  另建置 macOS 的 .app 與 .pkg, 以及 Windows 的 .msi.
#
# 建置順序為遠端程式, 前端與第三方授權清單, GUI 執行檔, 最後打包. GUI 以
# go:embed 內嵌剛建好的遠端程式. 第三方授權清單每次建置皆重新產生, 寫入
# frontend/src/third-party-licenses.json, 產生過程不需網路.
#
# 產出放 bin/, 檔名帶完整版本字串 x.y.z[-stage.N]+<commit>[-dirty]:
#   gpumon-<完整版本>-linux-amd64.bin
#   gpumon-<完整版本>-darwin-arm64
#   gpumon-<完整版本>-windows-amd64.exe
#   gpumon-<完整版本>-darwin-arm64.app    (--all)
#   gpumon-<完整版本>-darwin-arm64.pkg    (--all)
#   gpumon-<完整版本>-windows-amd64.msi   (--all)
#   gpumon                                指向本次 darwin 執行檔的符號連結
# 建置前先將 bin/ 內的舊產出依版本段歸檔至 bin/archive/<版本>/.

# 取得本檔路徑並判斷是否被 source. bash 與 zsh 取法不同, zsh 的語法以 eval 包住,
# 否則 bash 在解析階段即報錯.
BUILD_SOURCED=0
if [ -n "${ZSH_VERSION:-}" ]; then
    eval 'BUILD_SCRIPT=${(%):-%x}'
    case "${ZSH_EVAL_CONTEXT:-}" in
        *file*) BUILD_SOURCED=1 ;;
    esac
elif [ -n "${BASH_VERSION:-}" ]; then
    BUILD_SCRIPT=${BASH_SOURCE[0]}
    if [ "${BASH_SOURCE[0]}" != "$0" ]; then
        BUILD_SOURCED=1
    fi
else
    echo "Error: build.sh must be run with bash or zsh." >&2
    exit 1
fi

# 本檔會切換目錄並在錯誤時結束, 被 source 時會影響呼叫端的 shell, 故拒絕執行.
if [ "$BUILD_SOURCED" = 1 ]; then
    echo "Error: run build.sh with bash or zsh instead of sourcing it." >&2
    return 1
fi

set -eo pipefail

# 萬用字元沒有符合的檔案時展開為空, 兩種 shell 的預設行為不同, 於此統一.
if [ -n "${ZSH_VERSION:-}" ]; then
    setopt NULL_GLOB
else
    shopt -s nullglob
fi

cd "$(dirname "$BUILD_SCRIPT")"
ROOT_DIR=$(pwd)

# usage 輸出用法說明.
function usage {
    echo "usage: build.sh [-a|--all]"
    echo "  -a, --all  also build the .app, .pkg and .msi installers"
}

BUILD_ALL=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        -a|--all) BUILD_ALL=1 ;;
        *)
            echo "Error: unknown option: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
    shift
done

OUT_DIR=bin
APP_NAME="GPU Monitor"
BUNDLE_ID=io.github.nexgus.gpumon
# macOS 最低版本取 Go 1.27 連結器支援的最舊版本, 見
# $(go env GOROOT)/src/cmd/link/internal/ld/macho.go 的 macOS 預設值. 升級 Go 後
# 若出現 "built for newer macOS version than being linked" 警告, 須同步調整.
MACOS_MIN=13.0
LICENSES_JSON=frontend/src/third-party-licenses.json

# 讀取版本並檢查格式, 各數字不補零.
VERSION_FILE=internal/version/version.go
if [ "$(grep -c '^const VERSION = ' "$VERSION_FILE")" != 1 ]; then
    echo "Error: expected exactly one VERSION constant in $VERSION_FILE." >&2
    exit 1
fi
VER=$(sed -n 's/^const VERSION = "\(.*\)"$/\1/p' "$VERSION_FILE")
NUM='(0|[1-9][0-9]*)'
if ! printf '%s\n' "$VER" | grep -Eqx "${NUM}\.${NUM}\.${NUM}(-(alpha|beta|rc)\.${NUM})?"; then
    echo "Error: VERSION \"$VER\" in $VERSION_FILE is not of the form x.y.z[-stage.N]." >&2
    exit 1
fi

# Commit hash 取前 7 個字元, 工作目錄有未提交的修改 (含未追蹤的檔案) 時加上
# -dirty; 不在 git 儲存庫內或尚無 commit 時為 unknown.
if HEAD_HASH=$(git rev-parse --verify -q HEAD 2>/dev/null); then
    COMMIT=$(printf '%s' "$HEAD_HASH" | cut -c1-7)
    if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
        COMMIT="${COMMIT}-dirty"
    fi
else
    COMMIT=unknown
fi
FULLVER="${VER}+${COMMIT}"
# MSI Product Version 與 Info.plist 的版本欄位只接受純數字.
PUREVER="${VER%%-*}"
# 建置時間為 ISO 8601 extended 格式, BSD date 的 %z 不含冒號, 以 sed 補上.
BUILDDATE=$(date +%Y-%m-%dT%H:%M:%S%z | sed -E 's/([+-][0-9]{2})([0-9]{2})$/\1:\2/')
VERLD="-X gpumon/internal/version.GitCommitHash=${COMMIT} -X gpumon/internal/version.BuildDate=${BUILDDATE}"

CLI_OUT="gpumon-${FULLVER}-linux-amd64.bin"
MAC_OUT="gpumon-${FULLVER}-darwin-arm64"
WIN_OUT="gpumon-${FULLVER}-windows-amd64.exe"
APP_OUT="gpumon-${FULLVER}-darwin-arm64.app"
PKG_OUT="gpumon-${FULLVER}-darwin-arm64.pkg"
MSI_OUT="gpumon-${FULLVER}-windows-amd64.msi"

WORK_DIR=$(mktemp -d -t gpumon-build)
trap 'rm -rf "$WORK_DIR"' EXIT

# require_command 確認指定的命令存在, 否則中止.
function require_command {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "Error: $1 not found in PATH." >&2
        exit 1
    fi
}

# ensure_wixl 確保 wixl 已安裝, 缺少時以 Homebrew 安裝 msitools.
function ensure_wixl {
    if command -v wixl >/dev/null 2>&1; then
        return
    fi
    echo "wixl not found, installing msitools via Homebrew..."
    require_command brew
    brew install msitools
}

# ensure_npm_deps 於 node_modules 不存在或比 package-lock.json 舊時以 npm ci 安裝.
function ensure_npm_deps {
    if [ -f frontend/node_modules/.package-lock.json ] \
        && ! [ frontend/package-lock.json -nt frontend/node_modules/.package-lock.json ]; then
        return
    fi
    echo "Installing frontend dependencies (npm ci)..."
    (cd frontend && npm ci)
}

# archive_old_outputs 將 bin/ 內的舊產出依檔名中的版本段搬至
# bin/archive/<版本>/, 同名者覆蓋. 不符合 gpumon-* 樣式的項目原地保留, 版本段
# 無法解析者歸入 bin/archive/unknown/.
function archive_old_outputs {
    mkdir -p "$OUT_DIR/archive"
    local f base ver
    for f in "$OUT_DIR"/*; do
        base=$(basename "$f")
        case "$base" in
            archive) continue ;;
            gpumon-*) ;;
            *) continue ;;
        esac
        ver=$(printf '%s\n' "$base" | sed -E -n 's/^gpumon-([0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc)\.[0-9]+)?\+([0-9a-f]{7}|unknown)(-dirty)?)-.*/\1/p')
        if [ -z "$ver" ]; then
            ver=unknown
        fi
        mkdir -p "$OUT_DIR/archive/$ver"
        rm -rf "$OUT_DIR/archive/$ver/$base"
        mv "$f" "$OUT_DIR/archive/$ver/"
        echo "  archived $base -> archive/$ver/"
    done
}

# build_frontend 以 Vite 建置前端, 並將實際打包進產出的 npm 套件寫入 $1.
function build_frontend {
    (cd frontend && GPUMON_NPM_PACKAGES="$1" npm run build)
}

# build_app 組出 .app bundle. 圖示有兩份, CFBundleIconName 指向 assets/icon/Assets.car
# (含深色外觀), CFBundleIconFile 指向 assets/icon/gpumon.icns, 兩個鍵與 actool 產生的
# Info.plist 片段相同.
function build_app {
    local app="$OUT_DIR/$APP_OUT"
    rm -rf "$app"
    mkdir -p "$app/Contents/MacOS"
    mkdir -p "$app/Contents/Resources"
    cp "$OUT_DIR/$MAC_OUT" "$app/Contents/MacOS/gpumon"
    cp assets/icon/gpumon.icns "$app/Contents/Resources/gpumon.icns"
    cp assets/icon/Assets.car "$app/Contents/Resources/Assets.car"
    cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>${BUNDLE_ID}</string>
	<key>CFBundleName</key>
	<string>${APP_NAME}</string>
	<key>CFBundleDisplayName</key>
	<string>${APP_NAME}</string>
	<key>CFBundleExecutable</key>
	<string>gpumon</string>
	<key>CFBundleIconFile</key>
	<string>gpumon</string>
	<key>CFBundleIconName</key>
	<string>gpumon</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleVersion</key>
	<string>${PUREVER}</string>
	<key>CFBundleShortVersionString</key>
	<string>${PUREVER}</string>
	<key>LSMinimumSystemVersion</key>
	<string>${MACOS_MIN}</string>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
PLIST
    plutil -lint -s "$app/Contents/Info.plist"
}

# build_pkg 將 .app 打包為安裝至 /Applications 的 .pkg, 不簽章. 元件設為不可
# 重新定位, 使 Installer 固定安裝至 /Applications, 不追蹤其他位置的同 bundle
# identifier 副本.
function build_pkg {
    local root="$WORK_DIR/pkgroot" plist="$WORK_DIR/component.plist"
    local component="$WORK_DIR/gpumon-component.pkg" dist="$WORK_DIR/distribution.xml"
    rm -rf "$root"
    mkdir -p "$root"
    cp -R "$OUT_DIR/$APP_OUT" "$root/${APP_NAME}.app"
    pkgbuild --analyze --root "$root" "$plist" >/dev/null
    plutil -replace 0.BundleIsRelocatable -bool NO "$plist"
    pkgbuild --quiet --root "$root" --component-plist "$plist" \
        --identifier "$BUNDLE_ID" --version "$PUREVER" \
        --install-location /Applications "$component"
    productbuild --quiet --synthesize --package "$component" "$dist"
    # 加上安裝程式視窗的標題, 並將可安裝的硬體限定為 arm64, 因執行檔只含 arm64.
    sed -i '' \
        -e "s|<installer-gui-script minSpecVersion=\"[0-9]*\">|&<title>${APP_NAME}</title>|" \
        -e 's|hostArchitectures="[^"]*"|hostArchitectures="arm64"|' \
        "$dist"
    productbuild --quiet --distribution "$dist" --package-path "$WORK_DIR" "$OUT_DIR/$PKG_OUT"
}

# build_msi 由 msi/gui.wxs.in 產出 msi/gui.wxs, 再以 wixl 編譯 .msi, 不簽章.
function build_msi {
    sed -e "s/@VERSION@/${PUREVER}/g" -e "s/@FULLVERSION@/${FULLVER}/g" \
        msi/gui.wxs.in > msi/gui.wxs
    wixl -a x64 -o "$OUT_DIR/$MSI_OUT" msi/gui.wxs
}

require_command go
require_command npm
if [ "$BUILD_ALL" = 1 ]; then
    ensure_wixl
fi
ensure_npm_deps
go mod download
go -C scripts/licgen mod download
go -C scripts/syso mod download

echo "=== Building gpumon ${FULLVER} ==="
mkdir -p "$OUT_DIR"
echo "Archiving previous outputs..."
archive_old_outputs

echo "Building remote program (linux/amd64)..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w ${VERLD}" -o "$OUT_DIR/$CLI_OUT" ./cmd/cli
cp "$OUT_DIR/$CLI_OUT" internal/remotebin/gpumon.bin

echo "Generating frontend bindings..."
go tool wails3 generate bindings -ts -clean=true -silent -d frontend/bindings ./cmd/gui

echo "Building frontend..."
NPM_LIST="$WORK_DIR/npm-packages.tsv"
build_frontend "$NPM_LIST"

# 授權清單讀取的 Go module 與 npm 套件皆已在本機, 以 GOPROXY=off 確保不連網.
echo "Generating third-party license list..."
GOPROXY=off go -C scripts/licgen run . -root "$ROOT_DIR" -npm "$NPM_LIST" > "$WORK_DIR/licenses.json"
if cmp -s "$WORK_DIR/licenses.json" "$LICENSES_JSON"; then
    echo "Third-party license list unchanged."
else
    mv "$WORK_DIR/licenses.json" "$LICENSES_JSON"
    # 清單本身是前端的資料檔, 內容變動後重新建置前端, 使產出含最新清單.
    echo "Third-party license list updated, rebuilding frontend..."
    build_frontend "$NPM_LIST"
fi

echo "Building GUI (darwin/arm64)..."
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 \
    CGO_CFLAGS="-mmacosx-version-min=${MACOS_MIN}" \
    CGO_LDFLAGS="-mmacosx-version-min=${MACOS_MIN}" \
    go build -trimpath -tags production \
    -ldflags "-s -w -extldflags '-mmacosx-version-min=${MACOS_MIN}' ${VERLD}" \
    -o "$OUT_DIR/$MAC_OUT" ./cmd/gui
# 以相對路徑建立指向本次 macOS 執行檔的符號連結, 供直接以 bin/gpumon 執行最新的
# 建置; 既有的連結由新的連結取代.
ln -sfn "$MAC_OUT" "$OUT_DIR/gpumon"

# 由 assets/icon/gpumon.ico 產生只含圖示資源的 .syso, 檔名帶 windows_amd64 使 Go
# 只在該平台連結. 所需的 module 皆已在本機, 以 GOPROXY=off 確保不連網.
echo "Generating Windows icon resource..."
GOPROXY=off go -C scripts/syso run . -ico "$ROOT_DIR/assets/icon/gpumon.ico" \
    -out "$ROOT_DIR/cmd/gui/rsrc_windows_amd64.syso"

echo "Building GUI (windows/amd64)..."
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -tags production \
    -ldflags "-s -w -H windowsgui ${VERLD}" \
    -o "$OUT_DIR/$WIN_OUT" ./cmd/gui

if [ "$BUILD_ALL" = 1 ]; then
    echo "Assembling ${APP_OUT}..."
    build_app
    echo "Building ${PKG_OUT}..."
    build_pkg
    echo "Building ${MSI_OUT}..."
    build_msi
fi

echo
echo "Built artifacts (${OUT_DIR}/):"
for f in "$CLI_OUT" "$MAC_OUT" "$WIN_OUT"; do
    echo "  $f"
done
echo "  gpumon -> $MAC_OUT"
if [ "$BUILD_ALL" = 1 ]; then
    for f in "$APP_OUT" "$PKG_OUT" "$MSI_OUT"; do
        echo "  $f"
    done
fi
