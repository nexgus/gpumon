#!/bin/bash
# 印出最後一次改動產品路徑的 commit, 供 build.sh 與 release.sh 共用, 使只改動
# 文件等非產品內容的 commit 不改變完整版本字串中的 commit hash.
#
# 用法: bash scripts/product_commit.sh [--full]
#   不傳入選項時印出 commit hash 的前 7 個字元, 產品路徑有未提交的修改 (含
#   未追蹤的檔案) 時加上 -dirty.
#   --full  印出完整的 commit hash, 不加 -dirty.
# 不在 git 儲存庫內, 或沒有任何 commit 改動過產品路徑時, 不輸出任何內容並以
# 非零值結束, 由呼叫端決定替代值.
#
# PRODUCT_PATHS 列出內容會進入產出的路徑, 包括原始碼, 內嵌的素材, 授權清單的
# 來源, 安裝包範本與建置工具. 新增此類路徑時須同步更新本清單; 漏列會使產品
# 改變而 hash 不變, 故寧可多列.
PRODUCT_PATHS=(
    cmd
    internal
    frontend
    assets
    licenses
    msi/gui.wxs.in
    go.mod
    go.sum
    build.sh
    scripts/licgen
    scripts/syso
    scripts/product_commit.sh
)

# 判斷本檔是否被 source. bash 與 zsh 取法不同, zsh 的語法以 eval 包住, 否則 bash
# 在解析階段即報錯.
PC_SOURCED=0
if [ -n "${ZSH_VERSION:-}" ]; then
    eval 'PC_SCRIPT=${(%):-%x}'
    case "${ZSH_EVAL_CONTEXT:-}" in
        *file*) PC_SOURCED=1 ;;
    esac
elif [ -n "${BASH_VERSION:-}" ]; then
    PC_SCRIPT=${BASH_SOURCE[0]}
    if [ "${BASH_SOURCE[0]}" != "$0" ]; then
        PC_SOURCED=1
    fi
else
    echo "Error: product_commit.sh must be run with bash or zsh." >&2
    exit 1
fi

# 本檔會切換目錄並以 exit 結束, 被 source 時會影響呼叫端的 shell, 故拒絕執行.
if [ "$PC_SOURCED" = 1 ]; then
    echo "Error: run product_commit.sh with bash or zsh instead of sourcing it." >&2
    return 1
fi

set -eo pipefail

FULL=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --full) FULL=1 ;;
        *)
            echo "Error: unknown option: $1" >&2
            echo "usage: product_commit.sh [--full]" >&2
            exit 2
            ;;
    esac
    shift
done

cd "$(dirname "$PC_SCRIPT")/.."

HASH=$(git log -1 --format=%H -- "${PRODUCT_PATHS[@]}" 2>/dev/null || true)
if [ -z "$HASH" ]; then
    exit 1
fi

if [ "$FULL" = 1 ]; then
    echo "$HASH"
    exit 0
fi

SHORT=$(printf '%s' "$HASH" | cut -c1-7)
if [ -n "$(git status --porcelain -- "${PRODUCT_PATHS[@]}" 2>/dev/null)" ]; then
    SHORT="${SHORT}-dirty"
fi
echo "$SHORT"
