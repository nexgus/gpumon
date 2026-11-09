// 關於視窗的啟動: 讀取設定以套用外觀與語言, 並於設定變更時即時套用. 關於視窗不使用
// 主視窗的狀態 (state/app.ts), 不訂閱連線相關的事件.
import { StrictMode } from "react";
import type { Root } from "react-dom/client";
import { backend } from "../api/backend";
import type { AboutTabName } from "../api/types";
import { setLanguage } from "../i18n";
import { setAppearance } from "../theme";
import { AboutApp } from "./AboutApp";
import "../styles/about.css";

export async function startAbout(root: Root, tab: AboutTabName): Promise<void> {
  backend.on("app:ui-settings", (ui) => {
    setAppearance(ui.appearance);
    setLanguage(ui.language);
  });
  try {
    const s = await backend.getSettings();
    setAppearance(s.appearance);
    setLanguage(s.language);
  } finally {
    root.render(
      <StrictMode>
        <AboutApp initialTab={tab} />
      </StrictMode>,
    );
  }
}
