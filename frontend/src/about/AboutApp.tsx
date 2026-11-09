// 關於視窗: 頂端為 "關於" 與 "第三方授權" 兩個分頁的切換列, 其下為分頁內容, 內容超出
// 視窗高度時只有內容區捲動. 開啟時顯示的分頁由網址參數決定, 視窗已開啟時由 Go 端以
// app:about-tab 事件切換.
import { Fragment, useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { backend } from "../api/backend";
import type { AboutInfo, AboutTabName } from "../api/types";
import { formatDateTime } from "../format";
import { useT, type Translator } from "../i18n";
import appIcon from "./app-icon.png";
import { licenseView, paragraphs, type Component } from "./licenses";

// webViewVersion 由 user agent 取出 WebView 的版本: Windows 的 WebView2 為 Edg/<版本>,
// macOS 的 WKWebView 為 AppleWebKit/<版本>.
function webViewVersion(): string {
  const ua = navigator.userAgent;
  for (const re of [/\bEdg\/[\d.]+/, /\bAppleWebKit\/[\d.]+/, /\bChrome\/[\d.]+/]) {
    const m = re.exec(ua);
    if (m) {
      return m[0];
    }
  }
  return ua;
}

// formatBuilt 把 ISO 8601 的建置時間以本機時區顯示為 YYYY-MM-DD HH:MM:SS; 無法解析時
// (例如未經建置腳本時的 unknown) 照原文顯示.
function formatBuilt(s: string): string {
  const ms = Date.parse(s);
  return Number.isNaN(ms) ? s : formatDateTime(ms * 1000);
}

function infoRows(info: AboutInfo, t: Translator): [string, string][] {
  const rows: [string, string][] = [
    [t("about.version"), info.version],
    [t("about.built"), formatBuilt(info.build_date)],
    [t("about.go"), info.go_version],
    [t("about.os"), [info.os_name, info.os_version].filter(Boolean).join(" ") || t("about.unknown")],
    [t("about.platform"), info.platform],
    [t("about.webview"), webViewVersion()],
  ];
  if (info.platform.startsWith("windows/")) {
    rows.push([t("about.webview2"), info.webview2_version || t("about.unknown")]);
  }
  return rows;
}

function AppGlyph() {
  return <img src={appIcon} alt="" aria-hidden="true" width={64} height={64} />;
}

function AboutPanel() {
  const t = useT();
  const [info, setInfo] = useState<AboutInfo | null>(null);
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    void backend.about().then(setInfo);
  }, []);
  useEffect(() => {
    if (!copied) {
      return;
    }
    const id = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(id);
  }, [copied]);
  if (!info) {
    return <div className="about-content" />;
  }
  const rows = infoRows(info, t);
  const copy = () => {
    const text = [info.name, ...rows.map(([label, value]) => t("about.copyLine", { label, value }))].join("\n");
    void backend.copyText(text).then(() => setCopied(true));
  };
  return (
    <div className="about-content about-panel" role="tabpanel">
      <div className="about-head">
        <div className="about-glyph">
          <AppGlyph />
        </div>
        <div className="about-name">
          <span className="about-title">{info.name}</span>
          <span className="mono about-version">{info.version}</span>
        </div>
      </div>
      <dl className="about-facts">
        {rows.map(([label, value]) => (
          <div key={label} className="about-fact">
            <dt>{label}</dt>
            <dd className="mono">{value}</dd>
          </div>
        ))}
      </dl>
      <div className="flex1" />
      <div className="about-actions">
        <button type="button" className="btn" onClick={copy}>
          {copied ? t("about.copied") : t("about.copy")}
        </button>
      </div>
    </div>
  );
}

function ComponentTable({ items, onLicense }: { items: Component[]; onLicense: (id: string) => void }) {
  const t = useT();
  const open = (e: MouseEvent, url: string) => {
    e.preventDefault();
    void backend.openURL(url);
  };
  return (
    <div className="about-card">
      <table className="license-table">
        <thead>
          <tr>
            <th scope="col">{t("licenses.component")}</th>
            <th scope="col">{t("licenses.license")}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((c) => (
            <tr key={c.name}>
              <td className="mono">
                <a href={c.url} onClick={(e) => open(e, c.url)}>
                  {c.name}
                </a>
              </td>
              <td className="license-kind">
                <a
                  href={`#${c.textID}`}
                  onClick={(e) => {
                    e.preventDefault();
                    onLicense(c.textID);
                  }}
                >
                  {c.license}
                </a>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// LicensesPanel 為第三方授權分頁. 授權條文一律為英文原文, 不隨語言設定翻譯.
function LicensesPanel() {
  const t = useT();
  const view = useMemo(licenseView, []);
  const scroller = useRef<HTMLDivElement>(null);
  const jump = (id: string) => {
    const el = scroller.current?.querySelector<HTMLElement>(`#${id}`);
    el?.scrollIntoView({ block: "start" });
  };
  return (
    <div ref={scroller} className="about-content licenses-panel" role="tabpanel">
      <div className="license-heading">{t("licenses.direct")}</div>
      <ComponentTable items={view.direct} onLicense={jump} />
      {view.indirect.length > 0 && (
        <>
          <div className="license-heading">{t("licenses.indirect")}</div>
          <ComponentTable items={view.indirect} onLicense={jump} />
        </>
      )}
      <div className="license-texts" lang="en">
        {view.texts.map((lt) => (
          <section key={lt.id} id={lt.id} className="license-text">
            <div className="license-heading">{lt.title}</div>
            <div className="mono license-notices">
              {lt.notices.map((n) => (
                <div key={n.name} className="license-notice">
                  {n.text ? `${n.name} - ${n.text}` : n.name}
                </div>
              ))}
            </div>
            {paragraphs(lt.body).map((lines, i) => (
              <p key={i} className="license-body">
                {lines.map((line, k) => (
                  <Fragment key={k}>
                    {k > 0 && <br />}
                    {line}
                  </Fragment>
                ))}
              </p>
            ))}
          </section>
        ))}
      </div>
    </div>
  );
}

export function AboutApp({ initialTab }: { initialTab: AboutTabName }) {
  const t = useT();
  const [tab, setTab] = useState<AboutTabName>(initialTab);
  useEffect(() => backend.on("app:about-tab", (e) => setTab(e.tab === "licenses" ? "licenses" : "about")), []);
  const tabs: [AboutTabName, string][] = [
    ["about", t("about.tabAbout")],
    ["licenses", t("about.tabLicenses")],
  ];
  return (
    <div className="about">
      <div role="tablist" className="about-tabs">
        {tabs.map(([name, label]) => (
          <button
            key={name}
            type="button"
            role="tab"
            aria-selected={tab === name}
            className="about-tab"
            onClick={() => setTab(name)}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === "about" ? <AboutPanel /> : <LicensesPanel />}
    </div>
  );
}
