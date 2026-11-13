import { useMemo } from "react";
import type { HostStatus } from "../api/types";
import { StatusIcon } from "../components/StatusIcon";
import { Tab, TabList } from "../components/Tabs";
import { useT } from "../i18n";
import {
  choicesStore,
  closeTab,
  defaultCurves,
  hostsStore,
  liveStore,
  monitorTabsStore,
  selectHost,
  setMonitorSpan,
  settingsStore,
  tabHosts,
  uiStore,
} from "../state/app";
import { useStore } from "../state/store";
import { ZoomControls } from "./controls";
import { HostBlock } from "./HostBlock";

const defaultSpanMS = 300_000;
const defaultMinMS = 60_000;
const defaultMaxMS = 86_400_000;

// zoomMonitor 以 factor 倍變更監看畫面的顯示範圍長度, 限制於上下限之間. 右緣維持為最新
// 一筆取樣的時刻, 只改變左緣. 連續的滾輪事件可能發生於重新繪製之前, 長度以設定的最新值
// 為準.
function zoomMonitor(factor: number): void {
  const c = choicesStore.get();
  const min = c?.min_span_ms ?? defaultMinMS;
  const max = c?.max_monitor_span_ms ?? defaultMaxMS;
  const cur = settingsStore.get()?.monitor_span_ms ?? defaultSpanMS;
  setMonitorSpan(Math.min(max, Math.max(min, cur * factor)));
}

const onChartZoom = (factor: number) => zoomMonitor(factor);

// onSpan 把縮放按鍵回報的長度 (微秒) 設為監看畫面的顯示範圍長度.
const onSpan = (span: number) => setMonitorSpan(span / 1000);

// HostContent 為選取中主機的內容; 顯示範圍的右緣為該主機最新一筆取樣的時刻.
function HostContent({ host }: { host: HostStatus }) {
  const to = useStore(liveStore, (m) => (m[host.id]?.has_sample ? m[host.id].time : 0));
  const collapsed = useStore(settingsStore, (s) => s?.collapsed_gpus?.[host.id]);
  const procsCollapsed = useStore(settingsStore, (s) => s?.collapsed_procs?.includes(host.id) ?? false);
  const spanMS = useStore(settingsStore, (s) => s?.monitor_span_ms) ?? defaultSpanMS;
  const curves = useStore(settingsStore, (s) => s?.curves) ?? defaultCurves;
  return (
    <HostBlock
      host={host}
      span={spanMS * 1000}
      to={to}
      curves={curves}
      collapsedGPUs={collapsed}
      procsCollapsed={procsCollapsed}
      onZoom={onChartZoom}
    />
  );
}

// MonitorView 為監看畫面: 頂端為主機分頁與縮放控制項, 其下為選取中主機的 GPU 卡片與程序
// 卡片. 分頁只列出正在連線, 連線中, 重新連線中的主機與保留的分頁, 依側欄的主機順序; 保留的
// 分頁於名稱右側帶有關閉鍵. 已按下 "連線" 而後端尚未回報的主機視為正在連線.
export function MonitorView() {
  const t = useT();
  const hosts = useStore(hostsStore, (h) => h);
  const tabsState = useStore(monitorTabsStore, (s) => s);
  const selected = useStore(uiStore, (u) => u.selected);
  const tabs = useMemo(
    () =>
      tabHosts(hosts, tabsState).map((h): HostStatus => (tabsState.opening.has(h.id) && h.state === "disconnected" ? { ...h, state: "connecting" } : h)),
    [hosts, tabsState],
  );
  const current = tabs.find((h) => h.id === selected) ?? null;
  const spanMS = useStore(settingsStore, (s) => s?.monitor_span_ms) ?? defaultSpanMS;
  const minMS = useStore(choicesStore, (c) => c?.min_span_ms) ?? defaultMinMS;
  const maxMS = useStore(choicesStore, (c) => c?.max_monitor_span_ms) ?? defaultMaxMS;

  return (
    <main className="main">
      <div className="toolbar tabbar">
        <TabList label={t("monitor.hostTabs")} selectedKey={current?.id ?? null} className="flex1">
          {tabs.map((h) => (
            <Tab
              key={h.id}
              selected={h.id === current?.id}
              onSelect={() => selectHost(h.id)}
              onClose={h.state === "disconnected" ? () => closeTab(h.id) : undefined}
              closeLabel={t("monitor.closeTab", { name: h.name })}
            >
              <StatusIcon state={h.state} />
              <span className="mono">{h.name}</span>
            </Tab>
          ))}
        </TabList>
        <ZoomControls span={spanMS * 1000} min={minMS * 1000} max={maxMS * 1000} onSpan={onSpan} />
      </div>
      <div className="content">
        {tabs.length === 0 && <div className="empty-note">{t("monitor.noTabs")}</div>}
        {current && <HostContent key={current.id} host={current} />}
      </div>
    </main>
  );
}
