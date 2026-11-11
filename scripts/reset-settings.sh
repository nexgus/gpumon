#!/bin/bash
# 清除 GPU Monitor 的所有設定, 使程式下次啟動時以預設值啟動, 等同首次安裝.
# 記錄檔 (資料目錄下的 history/) 保留不動. 可用 bash 或 zsh 執行.
#
# 用法: bash reset-settings.sh [-y]
#   -y  不詢問, 直接刪除.
#
# 刪除的對象為資料目錄下的 settings.json, 以及程式留下的 settings.json.* 備份
# 與暫存檔. 資料目錄於 macOS 為 ~/Library/Application Support/gpumon, 其他平台
# 為 ${XDG_CONFIG_HOME:-~/.config}/gpumon; 環境變數 GPUMON_DATA_DIR 有值時以其
# 為準, 與 GUI 的判定方式相同.
#
# GUI 執行中時拒絕清除, 因 GUI 每次變更設定皆把記憶體中的完整設定寫回檔案,
# 刪除的設定會被寫回.

set -u

usage() {
    echo "Usage: bash reset-settings.sh [-y]"
    echo "  -y  Delete without asking for confirmation."
}

assume_yes=0
case "${1:-}" in
    "") ;;
    -y) assume_yes=1 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
esac

if [ -n "${GPUMON_DATA_DIR:-}" ]; then
    data_dir=$GPUMON_DATA_DIR
elif [ "$(uname -s)" = Darwin ]; then
    data_dir="$HOME/Library/Application Support/gpumon"
else
    data_dir="${XDG_CONFIG_HOME:-$HOME/.config}/gpumon"
fi

# GUI 的執行檔名為 gpumon (.app 內與 bin/ 下的符號連結) 或
# gpumon-<完整版本>-<平台> (build.sh 的直接產出). ps 的 comm 於 macOS 為完整
# 路徑, 路徑可含空白, 故以整行取最後一段比對.
running=$(ps -A -o comm= | awk '{ n = $0; sub(/.*\//, "", n); if (n ~ /^gpumon(-.*)?$/) print n }')
if [ -n "$running" ]; then
    echo "GPU Monitor is running. Quit it first, then run this script again." >&2
    exit 1
fi

# 以 find 列舉而不用萬用字元, 避免 zsh 於無相符檔案時報錯.
list_targets() {
    find "$data_dir" -maxdepth 1 -type f \( -name settings.json -o -name 'settings.json.*' \) 2>/dev/null
}

targets=$(list_targets)
if [ -z "$targets" ]; then
    echo "No settings found in $data_dir. Nothing to do."
    exit 0
fi

echo "The following files will be deleted:"
printf '%s\n' "$targets" | sed 's/^/  /'
echo "Recordings in $data_dir/history are kept."

if [ "$assume_yes" -ne 1 ]; then
    printf 'Continue? [y/N] '
    ans=""
    read -r ans || ans=""
    case "$ans" in
        y|Y|yes|YES|Yes) ;;
        *) echo "Cancelled."; exit 1 ;;
    esac
fi

find "$data_dir" -maxdepth 1 -type f \( -name settings.json -o -name 'settings.json.*' \) -delete
if [ -n "$(list_targets)" ]; then
    echo "Some files could not be deleted." >&2
    exit 1
fi
echo "Settings cleared. GPU Monitor will start with default settings."
