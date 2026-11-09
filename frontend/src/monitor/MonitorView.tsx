import { useEffect } from "react";
import type { Curve, HostStatus, TimeRange } from "../api/types";
import { StatusIcon } from "../components/StatusIcon";
import { rangeMicros } from "../format";
import { useT } from "../i18n";
import {
  choicesStore,
  defaultCurves,
  hostsStore,
  liveStore,
  selectHost,
  setHostRange,
  setSharedRange,
  setViewMode,
  settingsStore,
  uiStore,
} from "../state/app";
import { useStore } from "../state/store";
import { RangeSelect } from "./controls";
import { CurvesMenu } from "./CurvesMenu";
import { HostBlock } from "./HostBlock";

const monitorRanges: TimeRange[] = ["1m", "5m", "15m", "30m", "60m", "3h", "6h", "12h", "24h"];

function showsData(h: HostStatus): boolean {
  return h.state === "connected" || h.state === "reconnecting";
}

// TabsContent 為分頁顯示的內容, 只顯示選取中的主機; 顯示範圍的右緣為該主機最新
// 一筆取樣的時刻.
function TabsContent({ host, curves, ranges }: { host: HostStatus; curves: Curve[]; ranges: TimeRange[] }) {
  const to = useStore(liveStore, (m) => (m[host.id]?.has_sample ? m[host.id].time : 0));
  const collapsed = useStore(settingsStore, (s) => s?.collapsed_gpus?.[host.id]);
  const span = rangeMicros(host.range) || rangeMicros("5m");
  return (
    <HostBlock
      host={host}
      mode="tabs"
      from={to - span}
      to={to}
      curves={curves}
      collapsedGPUs={collapsed}
      range={host.range}
      ranges={ranges}
      onRange={(r) => void setHostRange(host.id, r)}
    />
  );
}

// AllContent 為同時顯示的內容. 所有主機共用一個顯示範圍, 右緣取各主機最新一筆取樣
// 時刻中的最晚者, 使所有曲線圖的時間軸上下對齊.
function AllContent({ hosts, curves, range }: { hosts: HostStatus[]; curves: Curve[]; range: TimeRange }) {
  const to = useStore(liveStore, (m) => {
    let latest = 0;
    for (const h of hosts) {
      const lv = m[h.id];
      if (showsData(h) && lv?.has_sample && lv.time > latest) {
        latest = lv.time;
      }
    }
    return latest;
  });
  const collapsedGPUs = useStore(settingsStore, (s) => s?.collapsed_gpus);
  const collapsedHosts = useStore(uiStore, (u) => u.collapsedHosts);
  const scrollTo = useStore(uiStore, (u) => u.scrollTo);
  const span = rangeMicros(range) || rangeMicros("5m");

  useEffect(() => {
    if (scrollTo) {
      document.getElementById(`host-${scrollTo.id}`)?.scrollIntoView({ block: "start", behavior: "smooth" });
    }
  }, [scrollTo]);

  return (
    <>
      {hosts.map((h) => (
        <HostBlock
          key={h.id}
          host={h}
          mode="all"
          from={to - span}
          to={to}
          curves={curves}
          collapsedGPUs={collapsedGPUs?.[h.id]}
          blockCollapsed={!!collapsedHosts[h.id]}
        />
      ))}
    </>
  );
}

// MonitorView 為監看畫面: 頂端為工具列, 其下為分頁顯示或同時顯示的主機區塊.
export function MonitorView() {
  const t = useT();
  const hosts = useStore(hostsStore, (h) => h);
  const viewMode = useStore(settingsStore, (s) => s?.view_mode) ?? "tabs";
  const sharedRange = useStore(settingsStore, (s) => s?.shared_range) ?? "5m";
  const curves = useStore(settingsStore, (s) => s?.curves) ?? defaultCurves;
  const ranges = useStore(choicesStore, (c) => c?.monitor_ranges) ?? monitorRanges;
  const selected = useStore(uiStore, (u) => u.selected);
  const current = hosts.find((h) => h.id === selected) ?? hosts[0];

  return (
    <main className="main">
      <div className="toolbar">
        <div role="group" aria-label={t("monitor.viewMode")} className="segmented">
          <button type="button" aria-pressed={viewMode === "tabs"} onClick={() => void setViewMode("tabs")}>
            {t("monitor.tabs")}
          </button>
          <button type="button" aria-pressed={viewMode === "all"} onClick={() => void setViewMode("all")}>
            {t("monitor.allHosts")}
          </button>
        </div>
        {viewMode === "tabs" && (
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
        )}
        <span className="flex1" />
        {viewMode === "all" && <RangeSelect value={sharedRange} ranges={ranges} onChange={(r) => void setSharedRange(r)} />}
        <CurvesMenu />
      </div>
      <div className="content">
        {hosts.length === 0 && <div className="empty-note">{t("monitor.noHosts")}</div>}
        {viewMode === "tabs" && current && <TabsContent key={current.id} host={current} curves={curves} ranges={ranges} />}
        {viewMode === "all" && <AllContent hosts={hosts} curves={curves} range={sharedRange} />}
      </div>
    </main>
  );
}
