import type { HostStatus } from "../api/types";
import { StatusIcon } from "../components/StatusIcon";
import { rangeMicros } from "../format";
import { useT } from "../i18n";
import { defaultCurves, hostsStore, liveStore, selectHost, settingsStore, uiStore } from "../state/app";
import { useStore } from "../state/store";
import { HostBlock } from "./HostBlock";

// HostContent 為選取中主機的內容; 顯示範圍的右緣為該主機最新一筆取樣的時刻.
function HostContent({ host }: { host: HostStatus }) {
  const to = useStore(liveStore, (m) => (m[host.id]?.has_sample ? m[host.id].time : 0));
  const collapsed = useStore(settingsStore, (s) => s?.collapsed_gpus?.[host.id]);
  const range = useStore(settingsStore, (s) => s?.monitor_range) ?? "5m";
  const curves = useStore(settingsStore, (s) => s?.curves) ?? defaultCurves;
  const span = rangeMicros(range) || rangeMicros("5m");
  return <HostBlock host={host} from={to - span} to={to} curves={curves} collapsedGPUs={collapsed} />;
}

// MonitorView 為監看畫面: 頂端為主機分頁, 其下為選取中主機的區塊.
export function MonitorView() {
  const t = useT();
  const hosts = useStore(hostsStore, (h) => h);
  const selected = useStore(uiStore, (u) => u.selected);
  const current = hosts.find((h) => h.id === selected) ?? hosts[0];

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
      </div>
      <div className="content">
        {hosts.length === 0 && <div className="empty-note">{t("monitor.noHosts")}</div>}
        {current && <HostContent key={current.id} host={current} />}
      </div>
    </main>
  );
}
