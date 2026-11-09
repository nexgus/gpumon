import { useId } from "react";
import type { Appearance, Language } from "../api/types";
import { useT } from "../i18n";
import { setAppearance, setLanguage, settingsStore } from "../state/app";
import { useStore } from "../state/store";

const appearances: Appearance[] = ["system", "light", "dark"];
const languages: Language[] = ["en", "zh-TW"];

// SettingsPage 為設定頁面, 含外觀與語言兩項, 選擇後立即生效並記住.
export function SettingsPage() {
  const t = useT();
  const appearance = useStore(settingsStore, (s) => s?.appearance) ?? "system";
  const language = useStore(settingsStore, (s) => s?.language) ?? "en";
  const labelID = useId();
  const langID = useId();
  return (
    <main className="main">
      <div className="toolbar">
        <span className="toolbar-title">{t("page.settings")}</span>
      </div>
      <div className="content">
        <div className="settings-card">
          <div className="settings-row">
            <div className="settings-text">
              <div id={labelID} className="settings-title">
                {t("settings.appearance")}
              </div>
              <div className="settings-hint">{t("settings.appearanceHint")}</div>
            </div>
            <div role="group" aria-labelledby={labelID} className="segmented">
              {appearances.map((a) => (
                <button key={a} type="button" aria-pressed={appearance === a} onClick={() => void setAppearance(a)}>
                  {t(`appearance.${a}`)}
                </button>
              ))}
            </div>
          </div>
          <div className="settings-row">
            <div className="settings-text">
              <label htmlFor={langID} className="settings-title">
                {t("settings.language")}
              </label>
              <div className="settings-hint">{t("settings.languageHint")}</div>
            </div>
            <select
              id={langID}
              className="select settings-select"
              value={language}
              onChange={(e) => void setLanguage(e.target.value as Language)}
            >
              {languages.map((l) => (
                <option key={l} value={l}>
                  {t(`language.${l}`)}
                </option>
              ))}
            </select>
          </div>
        </div>
      </div>
    </main>
  );
}
