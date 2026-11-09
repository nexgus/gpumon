#!/usr/bin/env bash
# 由圖示母版 (app.svg, app-foreground.svg, tray.svg, tray-eyes.svg) 產生所有衍生圖檔,
# 放在本目錄:
#   app-1024.png                           應用程式圖示的 1024 x 1024 點陣圖.
#   gpumon.icns                            macOS .app 使用的圖示.
#   gpumon.ico                             Windows 執行檔與 msi 使用的圖示.
#   frontend/src/about/app-icon.png        關於視窗顯示的圖示, 128 x 128, 保留透明背景.
#   tray-mac-<0-3>.png                     macOS 選單列的範本圖示 (黑色加 alpha), 44 x 44.
#   tray-win-light-<0-3>.ico               Windows 系統匣的淺色模式圖示.
#   tray-win-dark-<0-3>.ico                Windows 系統匣的深色模式圖示.
# 系統匣圖示兩個平台共用 tray.svg 與 tray-eyes.svg, 各尺寸皆直接由 SVG 渲染, 不由大圖縮小.
# Windows 系統匣圖示每個 .ico 含 16, 20, 24, 32, 48 像素五種尺寸, 各尺寸直接由 SVG
# 渲染, 不由大圖縮小; Wails 依系統的小圖示尺寸 (隨螢幕縮放比例而定) 挑選其中一張.
#   octopus-layer.png                      gpumon.icon 的前景圖層, 由 app-foreground.svg 產生.
#   Assets.car                             macOS 26 起使用的圖示, 由 gpumon.icon 編譯.
# 系統匣圖示的編號 0 至 3 為眨眼影格, 0 為睜眼, 3 為閉眼, 1 與 2 為眼睛以 35% 與
# 70% 的不透明度填回.
#
# gpumon.icon 為 Icon Composer 建立的文件, 背景為 #1d5fd1, 預設與深色外觀相同, 前景為
# octopus-layer.png 的副本. 本腳本只編譯它, 不修改它; 章魚造型更動時, 須在 Icon
# Composer 中以新產生的 octopus-layer.png 替換圖層並存檔, 再重新執行本腳本.
#
# 相依工具: Chrome (或 Chromium), ImageMagick 7 的 magick, macOS 的 sips 與 iconutil.
# 編譯 Assets.car 另需 Xcode 26 以上的 actool; 沒有時略過該步驟, 保留既有的 Assets.car.
# Chrome 的位置依序取環境變數 CHROME, /Applications/Google Chrome.app, Chromium.
#
# 執行方式: bash assets/icon/generate.sh
# 母版更動時須重新執行本腳本, 並一併提交所有衍生檔.
#
# 渲染 SVG 一律使用 Chrome, 不可讓 ImageMagick 直接讀 SVG. 本機的 ImageMagick 沒有
# rsvg delegate, 內建的 MSVG renderer 不支援 mask 且算不準 stroke, 會產出錯誤的形狀.

set -eo pipefail

ICON_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
WORK=$(mktemp -d -t gpumon-icon)
trap 'rm -rf "$WORK"' EXIT

find_chrome() {
    if [ -n "${CHROME:-}" ]; then
        printf '%s\n' "$CHROME"
        return
    fi
    local c
    for c in "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
        "/Applications/Chromium.app/Contents/MacOS/Chromium"; do
        if [ -x "$c" ]; then
            printf '%s\n' "$c"
            return
        fi
    done
    echo "Error: Chrome not found, set the CHROME environment variable." >&2
    return 1
}
CHROME=$(find_chrome)

# render_svg SVG PNG SIZE [背景色] 以 Chrome headless 將 SVG 渲染為 SIZE x SIZE 的 PNG,
# 逾 60 秒視為卡住並中止.
render_svg() {
    local svg=$1 png=$2 size=$3 bg=${4:-}
    local args=(--headless --disable-gpu --hide-scrollbars --force-device-scale-factor=1
        --window-size="$size,$size" --user-data-dir="$WORK/chrome-profile" --screenshot="$png")
    if [ -n "$bg" ]; then
        args+=(--default-background-color="$bg")
    fi
    if ! perl -e 'alarm 60; exec @ARGV' "$CHROME" "${args[@]}" "file://$svg" >"$WORK/chrome.log" 2>&1 \
        || [ ! -s "$png" ]; then
        echo "Error: failed to render $svg" >&2
        cat "$WORK/chrome.log" >&2
        return 1
    fi
}

# render_svg_at SVG PNG SIZE 以 CSS 將 SVG 直接排版為 SIZE x SIZE 後渲染, 小尺寸時可得到
# 對齊像素格的結果, 不經由大圖縮小. 用於系統匣的各尺寸.
render_svg_at() {
    local svg=$1 png=$2 size=$3
    {
        printf '<!doctype html><style>html,body{margin:0;background:#000}'
        printf 'svg{display:block;width:%spx;height:%spx}</style>' "$size" "$size"
        sed 's/<?xml[^>]*>//' "$svg"
    } >"$WORK/wrap.html"
    if ! perl -e 'alarm 60; exec @ARGV' "$CHROME" --headless --disable-gpu --hide-scrollbars \
        --force-device-scale-factor=1 --window-size="$size,$size" \
        --user-data-dir="$WORK/chrome-profile" --screenshot="$png" \
        "file://$WORK/wrap.html" >"$WORK/chrome.log" 2>&1 || [ ! -s "$png" ]; then
        echo "Error: failed to render $svg at $size px" >&2
        cat "$WORK/chrome.log" >&2
        return 1
    fi
    magick "$png" -colorspace gray "$png"
}

# mask_to_icon MASK 色彩 大小 輸出, 將黑底白形的遮罩轉為灰階, 作為 alpha 後填上色彩.
mask_to_icon() {
    magick "$1" -colorspace gray -alpha copy -fill "$2" -colorize 100 \
        -resize "$3x$3" "$4"
}

echo "Rendering masters..."
render_svg "$ICON_DIR/app.svg" "$ICON_DIR/app-1024.png" 1024 00000000
render_svg "$ICON_DIR/app-foreground.svg" "$ICON_DIR/octopus-layer.png" 1024 00000000

echo "Building gpumon.icns..."
ICONSET="$WORK/gpumon.iconset"
mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
    sips -z "$s" "$s" "$ICON_DIR/app-1024.png" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
    sips -z "$((s * 2))" "$((s * 2))" "$ICON_DIR/app-1024.png" --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$ICON_DIR/gpumon.icns"

echo "Building gpumon.ico..."
magick "$ICON_DIR/app-1024.png" -define icon:auto-resize=256,128,64,48,32,24,16 "$ICON_DIR/gpumon.ico"

echo "Building about window icon..."
magick "$ICON_DIR/app-1024.png" -resize 128x128 "$ICON_DIR/../../frontend/src/about/app-icon.png"

# eye_ratio N 印出影格 N 的眼睛填回比例, 為 0, 89/255, 178/255, 1.
eye_ratio() {
    case $1 in
        0) echo 0 ;;
        1) echo 0.349019608 ;;
        2) echo 0.698039216 ;;
        3) echo 1 ;;
    esac
}

echo "Building macOS tray frames..."
# 影格遮罩 = tray 遮罩 + eyes 遮罩 x 比例.
render_svg_at "$ICON_DIR/tray.svg" "$WORK/mac.png" 44
render_svg_at "$ICON_DIR/tray-eyes.svg" "$WORK/mac-eyes.png" 44
for n in 0 1 2 3; do
    magick "$WORK/mac.png" \( "$WORK/mac-eyes.png" -evaluate multiply "$(eye_ratio "$n")" \) \
        -compose plus -composite "$WORK/frame-$n.png"
    mask_to_icon "$WORK/frame-$n.png" '#000000' 44 "$ICON_DIR/tray-mac-$n.png"
done

echo "Building Windows tray frames..."
WIN_SIZES="16 20 24 32 48"
for s in $WIN_SIZES; do
    render_svg_at "$ICON_DIR/tray.svg" "$WORK/win-$s.png" "$s"
    render_svg_at "$ICON_DIR/tray-eyes.svg" "$WORK/win-eyes-$s.png" "$s"
done
for n in 0 1 2 3; do
    light=() dark=()
    for s in $WIN_SIZES; do
        magick "$WORK/win-$s.png" \( "$WORK/win-eyes-$s.png" -evaluate multiply "$(eye_ratio "$n")" \) \
            -compose plus -composite "$WORK/win-frame-$n-$s.png"
        mask_to_icon "$WORK/win-frame-$n-$s.png" '#15181c' "$s" "$WORK/win-light-$n-$s.png"
        mask_to_icon "$WORK/win-frame-$n-$s.png" '#e7eaee' "$s" "$WORK/win-dark-$n-$s.png"
        light+=("$WORK/win-light-$n-$s.png")
        dark+=("$WORK/win-dark-$n-$s.png")
    done
    magick "${light[@]}" -type TrueColorAlpha "$ICON_DIR/tray-win-light-$n.ico"
    magick "${dark[@]}" -type TrueColorAlpha "$ICON_DIR/tray-win-dark-$n.ico"
done

# actool_major 印出 actool 的主版本號, 沒有 actool 時印出 0.
actool_major() {
    if [ ! -x /usr/bin/actool ]; then
        echo 0
        return
    fi
    # 版本資訊為 plist, 鍵名含句點, 無法以 plutil 的路徑取值, 改以文字比對.
    local v
    v=$(/usr/bin/actool --version 2>/dev/null \
        | grep -A1 'short-bundle-version' | sed -n 's/.*<string>\([0-9]*\).*/\1/p')
    echo "${v:-0}"
}

if [ "$(actool_major)" -ge 26 ] 2>/dev/null; then
    echo "Building Assets.car..."
    mkdir -p "$WORK/car"
    /usr/bin/actool "$ICON_DIR/gpumon.icon" --compile "$WORK/car" \
        --notices --warnings --errors \
        --output-partial-info-plist "$WORK/car/partial.plist" \
        --app-icon gpumon --enable-on-demand-resources NO --development-region en \
        --target-device mac --minimum-deployment-target 13.0 --platform macosx >/dev/null
    cp "$WORK/car/Assets.car" "$ICON_DIR/Assets.car"
else
    echo "Warning: actool 26+ not found (install Xcode 26+), keeping the existing Assets.car." >&2
fi

echo "Outputs:"
for f in app-1024.png tray-mac-0.png tray-mac-3.png; do
    printf '  %s: ' "$f"
    magick identify -format '%wx%h %[channels]\n' "$ICON_DIR/$f"
done
for f in tray-win-light-0.ico tray-win-dark-3.ico; do
    printf '  %s: ' "$f"
    magick identify -format '%wx%h ' "$ICON_DIR/$f"
    echo
done
magick identify -format '  app-icon.png: %wx%h %[channels]\n' "$ICON_DIR/../../frontend/src/about/app-icon.png"
magick identify "$ICON_DIR/gpumon.ico"
