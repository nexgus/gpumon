import { useId } from "react";
import type { Appearance, Curve, Language, TimeRange } from "../api/types";
import { allCurves, curveColor } from "../chart/gpuSeries";
import { useT } from "../i18n";
import {
  choicesStore,
  defaultCurves,
  setAppearance,
  setCurves,
  setLanguage,
  setMonitorRange,
  settingsStore,
} from "../state/app";
import { useStore } from "../state/store";

const appearances: Appearance[] = ["system", "light", "dark"];
const languages: Language[] = ["en", "zh-TW"];
const monitorRanges: TimeRange[] = ["1m", "5m", "15m", "30m", "60m", "3h", "6h", "12h", "24h"];

// SettingsPage 為設定頁面, 含外觀, 語言, 監看畫面的顯示範圍與曲線四項, 選擇後立即生效並記住.
export function SettingsPage() {
  const t = useT();
  const appearance = useStore(settingsStore, (s) => s?.appearance) ?? "system";
  const language = useStore(settingsStore, (s) => s?.language) ?? "en";
  const labelID = useId();
  const langID = useId();
  const rangeID = useId();
  const curvesID = useId();
  const monitorRange = useStore(settingsStore, (s) => s?.monitor_range) ?? "5m";
  const ranges = useStore(choicesStore, (c) => c?.monitor_ranges) ?? monitorRanges;
  const curves = useStore(settingsStore, (s) => s?.curves) ?? defaultCurves;
  const maxCurves = useStore(choicesStore, (c) => c?.max_curves) ?? allCurves.length;
  const toggleCurve = (c: Curve, on: boolean) => {
    void setCurves(allCurves.filter((x) => (x === c ? on : curves.includes(x))));
  };
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
          <div className="settings-row">
            <div className="settings-text">
              <label htmlFor={rangeID} className="settings-title">
                {t("monitor.timeRange")}
              </label>
              <div className="settings-hint">{t("settings.timeRangeHint")}</div>
            </div>
            <select
              id={rangeID}
              className="select settings-select"
              value={monitorRange}
              onChange={(e) => void setMonitorRange(e.target.value as TimeRange)}
            >
              {ranges.map((r) => (
                <option key={r} value={r}>
                  {t(`range.${r}`)}
                </option>
              ))}
            </select>
          </div>
          <div className="settings-row top">
            <div className="settings-text">
              <div id={curvesID} className="settings-title">
                {t("monitor.curves")}
              </div>
              <div className="settings-hint">{t("settings.curvesHint")}</div>
            </div>
            <div role="group" aria-labelledby={curvesID} className="curves-list">
              {allCurves.map((c) => {
                const checked = curves.includes(c);
                const id = `curve-${c}`;
                return (
                  <div key={c} className="curves-item">
                    <input
                      id={id}
                      type="checkbox"
                      checked={checked}
                      disabled={!checked && curves.length >= maxCurves}
                      onChange={(e) => toggleCurve(c, e.target.checked)}
                    />
                    <span className="curves-swatch" style={{ background: `var(${curveColor[c]})` }} />
                    <label htmlFor={id}>{t(`curve.${c}`)}</label>
                  </div>
                );
              })}
            </div>
          </div>
        </div>
      </div>
    </main>
  );
}
