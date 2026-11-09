import { memo, useCallback, useMemo, useState } from "react";
import type { Curve, Device, HostStatus, Live, TimeRange } from "../api/types";
import type { ChartGap } from "../chart/Chart";
import { fieldsFor, gpuSeries, toChartData } from "../chart/gpuSeries";
import { ChevronDown, ChevronRight } from "../components/Icons";
import { StatusIcon } from "../components/StatusIcon";
import { formatClock } from "../format";
import { useT } from "../i18n";
import { connect, disconnect, emptyLive, liveStore, setGPUCollapsed, setHostCollapsed } from "../state/app";
import { useStore } from "../state/store";
import { IntervalSlider, RangeSelect } from "./controls";
import { GpuRow } from "./GpuRow";
import { statusClass, statusText } from "./hostText";
import { ProcessTable } from "./ProcessTable";
import { useCurveFeed } from "./useCurveFeed";

const noGPUs: number[] = [];
const noDevices: Device[] = [];

export interface HostBlockProps {
  host: HostStatus;
  mode: "tabs" | "all";
  // from 與 to 為曲線圖的顯示範圍 (Unix 微秒); to 為 0 表示尚無取樣.
  from: number;
  to: number;
  curves: Curve[];
  collapsedGPUs: number[] | undefined;
  // 以下只用於分頁顯示: 主機自己的顯示範圍與可選的範圍.
  range?: TimeRange;
  ranges?: TimeRange[];
  onRange?: (r: TimeRange) => void;
  // 以下只用於同時顯示: 區塊是否收合.
  blockCollapsed?: boolean;
}

function ConnectButton({ host }: { host: HostStatus }) {
  const t = useT();
  const [busy, setBusy] = useState(false);
  const run = async (fn: (id: string) => Promise<void>) => {
    setBusy(true);
    try {
      await fn(host.id);
    } finally {
      setBusy(false);
    }
  };
  if (host.state === "disconnected") {
    return (
      <button type="button" className="btn btn-primary" disabled={busy} onClick={() => run(connect)}>
        {t("action.connect")}
      </button>
    );
  }
  return (
    <button type="button" className="btn" disabled={busy} onClick={() => run(disconnect)}>
      {t("action.disconnect")}
    </button>
  );
}

// HostBlock 為監看畫面中一台主機的區塊: 標題列, 每張 GPU 的橫列與程序列表. 未連線與
// 正在連線的主機只顯示標題列; 重新連線中的主機照常顯示斷線前的讀數與曲線.
export const HostBlock = memo(function HostBlock(p: HostBlockProps) {
  const t = useT();
  const { host } = p;
  const live: Live = useStore(liveStore, (m) => m[host.id]) ?? emptyLive(host.id);
  const [chartWidth, setChartWidth] = useState(0);
  const onWidth = useCallback((w: number) => setChartWidth(w), []);
  const onToggleGPU = useCallback(
    (gpuID: number, collapsed: boolean) => void setGPUCollapsed(host.id, gpuID, collapsed),
    [host.id],
  );

  const showsData = host.state === "connected" || host.state === "reconnecting";
  const devices = live.devices;
  const gpus = devices?.gpus ?? noDevices;
  const collapsedGPUs = p.collapsedGPUs ?? noGPUs;
  const anyExpanded = gpus.some((g) => !collapsedGPUs.includes(g.id));
  const expanded = showsData && !p.blockCollapsed;
  const fields = useMemo(() => fieldsFor(p.curves), [p.curves]);
  const result = useCurveFeed(host.id, expanded && anyExpanded && p.to > 0, p.from, p.to, chartWidth, fields);

  // 重新連線中的主機於最後一筆取樣之後沒有資料; 同時顯示時該時段以無資料時段標示.
  const trailing = useMemo((): ChartGap[] => {
    if (host.state !== "reconnecting" || !live.has_sample) {
      return [];
    }
    const minGap = Math.max(1, live.interval_ms) * 3 * 1000;
    if (p.to - live.time <= minGap) {
      return [];
    }
    return [{ start: live.time, end: p.to, detail: host.reason || t("gap.unknown") }];
  }, [host.state, host.reason, live.has_sample, live.time, live.interval_ms, p.to, t]);

  const data = useMemo(() => (result ? toChartData(result, t, trailing) : null), [result, t, trailing]);
  const seriesByGPU = useMemo(() => {
    const m = new Map<number, ReturnType<typeof gpuSeries>>();
    if (result) {
      for (const g of gpus) {
        m.set(g.id, gpuSeries(result, g.id, g, p.curves, t));
      }
    }
    return m;
  }, [result, gpus, p.curves, t]);

  const hostname = devices?.hostname || host.addr;
  // 沒有可監看 GPU 的主機不顯示取樣間隔與顯示範圍, 依設計稿.
  const hasGPUs = showsData && !(devices && gpus.length === 0);
  const header = (
    <header className={`host-header ${p.mode}`}>
      {p.mode === "all" && (
        <button
          type="button"
          className="host-collapse"
          aria-expanded={!p.blockCollapsed}
          aria-label={t(p.blockCollapsed ? "action.expandHost" : "action.collapseHost", { name: host.name })}
          onClick={() => setHostCollapsed(host.id, !p.blockCollapsed)}
        >
          {p.blockCollapsed ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
        </button>
      )}
      <span className="host-hostname">{hostname}</span>
      <span className="host-name">{host.name}</span>
      <span className={`host-status ${statusClass(host)}`}>
        <StatusIcon state={host.state} />
        {statusText(host, t)}
      </span>
      <span className="flex1" />
      {hasGPUs && <IntervalSlider hostID={host.id} value={host.interval_ms} />}
      {hasGPUs && p.mode === "tabs" && p.range && p.ranges && p.onRange && (
        <RangeSelect value={p.range} ranges={p.ranges} onChange={p.onRange} />
      )}
      <ConnectButton host={host} />
    </header>
  );

  let body = null;
  if (expanded) {
    if (devices && gpus.length === 0) {
      body = <div className="host-note">{t("monitor.noGpu")}</div>;
    } else if (!devices) {
      body = <div className="host-note">{t("monitor.waiting")}</div>;
    } else {
      body = (
        <>
          {gpus.map((g) => (
            <GpuRow
              key={g.id}
              device={g}
              gpu={live.has_sample ? live.gpus?.find((x) => x.id === g.id) : undefined}
              collapsed={collapsedGPUs.includes(g.id)}
              onToggle={onToggleGPU}
              from={p.from}
              to={p.to}
              data={data}
              series={seriesByGPU.get(g.id) ?? []}
              onWidth={onWidth}
            />
          ))}
          <ProcessTable
            title={
              host.state === "reconnecting" && live.has_sample
                ? t("monitor.processesLast", { time: formatClock(live.time) })
                : t("monitor.processes")
            }
            procs={live.procs ?? []}
          />
        </>
      );
    }
  }

  return (
    <div className="host-block" id={`host-${host.id}`}>
      {header}
      {body}
    </div>
  );
});
