import type { HostStatus } from "../api/types";
import { StatusIcon } from "../components/StatusIcon";
import { useT } from "../i18n";
import { choicesStore, defaultCurves, hostsStore, liveStore, selectHost, setMonitorSpan, settingsStore, uiStore } from "../state/app";
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
// 卡片.
export function MonitorView() {
  const t = useT();
  const hosts = useStore(hostsStore, (h) => h);
  const selected = useStore(uiStore, (u) => u.selected);
  const current = hosts.find((h) => h.id === selected) ?? hosts[0];
  const spanMS = useStore(settingsStore, (s) => s?.monitor_span_ms) ?? defaultSpanMS;
  const minMS = useStore(choicesStore, (c) => c?.min_span_ms) ?? defaultMinMS;
  const maxMS = useStore(choicesStore, (c) => c?.max_monitor_span_ms) ?? defaultMaxMS;

  return (
    <main className="main">
      <div className="toolbar">
        <div role="tablist" aria-label={t("monitor.hostTabs")} className="host-tabs">
          {hosts.map((h) => (
            <button
              key={h.id}
              type="button"
              role="tab"
              aria-selected={h.id === current?.id}
              className="host-tab"
              onClick={() => selectHost(h.id)}
            >
              <StatusIcon state={h.state} />
              {h.name}
            </button>
          ))}
        </div>
        <span className="flex1" />
        <ZoomControls span={spanMS * 1000} min={minMS * 1000} max={maxMS * 1000} onZoom={zoomMonitor} />
      </div>
      <div className="content">
        {hosts.length === 0 && <div className="empty-note">{t("monitor.noHosts")}</div>}
        {current && <HostContent key={current.id} host={current} />}
      </div>
    </main>
  );
}
