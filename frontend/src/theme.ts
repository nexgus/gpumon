// 外觀的套用. 跟隨系統的做法依規格 6.1: 以 prefers-color-scheme 判斷, 深色時在 html
// 元素加上 dark class, 並監聽系統設定變化. 設定值為淺色或深色時不跟隨系統.
import type { Appearance } from "./api/types";

let appearance: Appearance = "system";
const themeListeners = new Set<() => void>();

function applyColorScheme(matches: boolean): void {
  document.documentElement.classList.toggle("dark", matches);
  for (const l of [...themeListeners]) {
    l();
  }
}

const darkMediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
applyColorScheme(darkMediaQuery.matches);
darkMediaQuery.addEventListener("change", (event: MediaQueryListEvent) => {
  if (appearance === "system") {
    applyColorScheme(event.matches);
  }
});

// setAppearance 套用外觀設定, 立即生效.
export function setAppearance(v: Appearance): void {
  appearance = v;
  if (v === "system") {
    delete document.documentElement.dataset.appearance;
  } else {
    document.documentElement.dataset.appearance = v;
  }
  applyColorScheme(v === "dark" || (v === "system" && darkMediaQuery.matches));
}

// onThemeChange 註冊外觀變更 (含系統切換) 時的處理函數, 供以 canvas 繪製者重畫.
export function onThemeChange(l: () => void): () => void {
  themeListeners.add(l);
  return () => themeListeners.delete(l);
}
