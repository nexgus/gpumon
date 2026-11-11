#!/bin/bash
# 將 bin/ 內對應目前產品內容的 .msi 與 .pkg 上傳至 GitHub, 並建立 release.
#
# 用法: bash release.sh [版本]
#   版本預設取自 internal/version/version.go 的 VERSION; 明確指定版本時須與其相同,
#   否則中止. 只發布正式版本 x.y.z, 帶 alpha, beta 或 rc 階段的版本一律中止.
#
# Release 的 tag 名稱為版本本身, 不加 v, 指向最後一次改動產品路徑的 commit (見
# scripts/product_commit.sh), 不一定是 HEAD. 產品路徑有未提交的修改, 該 commit
# 尚未 push 至 origin/main, 或 bin/ 內找不到以該 commit 建置的安裝包時, 中止發布.
#
# Release note 取 docs/releases/<版本>.md. 該檔不存在而開發期的
# docs/releases/<版本>-rc.md 存在時, 於全部檢查通過後更名為正式檔名並 commit,
# 只提交該檔, 不 push.
#
# 上傳的 asset 檔名去掉 commit hash, 例如 gpumon-0.1.0-windows-amd64.msi.

# 取得本檔路徑並判斷是否被 source. bash 與 zsh 取法不同, zsh 的語法以 eval 包住,
# 否則 bash 在解析階段即報錯.
RELEASE_SOURCED=0
if [ -n "${ZSH_VERSION:-}" ]; then
    eval 'RELEASE_SCRIPT=${(%):-%x}'
    case "${ZSH_EVAL_CONTEXT:-}" in
        *file*) RELEASE_SOURCED=1 ;;
    esac
elif [ -n "${BASH_VERSION:-}" ]; then
    RELEASE_SCRIPT=${BASH_SOURCE[0]}
    if [ "${BASH_SOURCE[0]}" != "$0" ]; then
        RELEASE_SOURCED=1
    fi
else
    echo "Error: release.sh must be run with bash or zsh." >&2
    exit 1
fi

# 本檔會切換目錄並在錯誤時結束, 被 source 時會影響呼叫端的 shell, 故拒絕執行.
if [ "$RELEASE_SOURCED" = 1 ]; then
    echo "Error: run release.sh with bash or zsh instead of sourcing it." >&2
    return 1
fi

set -eo pipefail

cd "$(dirname "$RELEASE_SCRIPT")"

FORGECTL=(forgectl -s github)
REPO=nexgus/gpumon
VERSION_FILE=internal/version/version.go

# die 輸出錯誤訊息後中止.
function die {
    echo "Error: $*" >&2
    exit 1
}

if [ "$#" -gt 1 ]; then
    echo "usage: release.sh [version]" >&2
    exit 2
fi

command -v forgectl >/dev/null 2>&1 || die "forgectl not found in PATH."

if [ "$(grep -c '^const VERSION = ' "$VERSION_FILE")" != 1 ]; then
    die "expected exactly one VERSION constant in $VERSION_FILE."
fi
FILE_VER=$(sed -n 's/^const VERSION = "\(.*\)"$/\1/p' "$VERSION_FILE")
VER="${1:-$FILE_VER}"
[ "$VER" = "$FILE_VER" ] \
    || die "version $VER does not match VERSION \"$FILE_VER\" in $VERSION_FILE."

NUM='(0|[1-9][0-9]*)'
printf '%s\n' "$VER" | grep -Eqx "${NUM}\.${NUM}\.${NUM}" \
    || die "version $VER is not a release version of the form x.y.z."

SHORT=$(bash scripts/product_commit.sh) \
    || die "cannot determine the product commit (scripts/product_commit.sh failed)."
case "$SHORT" in
    *-dirty) die "product paths have uncommitted changes ($SHORT); commit them first." ;;
esac
FULL=$(bash scripts/product_commit.sh --full) \
    || die "cannot determine the full product commit hash."

MSI="bin/gpumon-${VER}+${SHORT}-windows-amd64.msi"
PKG="bin/gpumon-${VER}+${SHORT}-darwin-arm64.pkg"
for f in "$MSI" "$PKG"; do
    [ -f "$f" ] || die "$f not found; run \"bash build.sh -a\" to build the installers for commit $SHORT."
done

NOTE="docs/releases/${VER}.md"
RC_NOTE="docs/releases/${VER}-rc.md"
[ -f "$NOTE" ] || [ -f "$RC_NOTE" ] \
    || die "release note $NOTE not found (nor $RC_NOTE)."

# 確認產品 commit 已 push 至 origin/main, 避免 tag 指向只存在於本機的 commit.
git fetch origin
git merge-base --is-ancestor "$FULL" origin/main \
    || die "commit $SHORT has not been pushed to origin/main; push it first."

# 全部檢查通過後才更名開發期的 release note 並 commit, 以免中止時留下多餘的
# commit. docs/releases 不在產品路徑內, 此 commit 不改變產品 commit.
if [ -f "$NOTE" ]; then
    if [ -f "$RC_NOTE" ]; then
        echo "Warning: both $NOTE and $RC_NOTE exist; using $NOTE." >&2
    fi
else
    COMMIT_PATHS=("$NOTE")
    if git ls-files --error-unmatch "$RC_NOTE" >/dev/null 2>&1; then
        git mv "$RC_NOTE" "$NOTE"
        COMMIT_PATHS+=("$RC_NOTE")
    else
        mv "$RC_NOTE" "$NOTE"
        git add -- "$NOTE"
    fi
    git commit -m "release note 更名為正式版 ${VER}" -- "${COMMIT_PATHS[@]}"
    echo "Renamed $RC_NOTE to $NOTE and committed it (not pushed)."
fi

echo "Releasing $VER (commit $SHORT) to github.com/$REPO..."
"${FORGECTL[@]}" asset upload "$REPO" "$VER" \
    "${MSI}=gpumon-${VER}-windows-amd64.msi" \
    "${PKG}=gpumon-${VER}-darwin-arm64.pkg"
"${FORGECTL[@]}" release create "$REPO" "$VER" -n "$NOTE" -c "$FULL"
git fetch --tags origin
