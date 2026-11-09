import { memo, useCallback, useMemo, useState } from "react";
import type { Curve, Device, HostStatus, Live } from "../api/types";
import { fieldsFor, gpuSeries, toChartData } from "../chart/gpuSeries";
import { formatClock } from "../format";
import { useT } from "../i18n";
import { emptyLive, liveStore, setGPUCollapsed, setProcessesCollapsed } from "../state/app";
import { useStore } from "../state/store";
import { GpuRow } from "./GpuRow";
import { ProcessTable } from "./ProcessTable";
import { useCurveFeed } from "./useCurveFeed";

const noGPUs: number[] = [];
const noDevices: Device[] = [];

export interface HostBlockProps {
  host: HostStatus;
  // from 與 to 為曲線圖的顯示範圍 (Unix 微秒); to 為 0 表示尚無取樣.
  from: number;
  to: number;
  curves: Curve[];
  collapsedGPUs: number[] | undefined;
  procsCollapsed: boolean;
}

// HostBlock 為監看畫面中一台主機的內容: 每張 GPU 一張卡片, 最後為程序卡片. 沒有 GPU
// 卡片時只顯示一行說明; 重新連線中的主機照常顯示斷線前的讀數與曲線.
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
  const onToggleProcs = useCallback((collapsed: boolean) => void setProcessesCollapsed(host.id, collapsed), [host.id]);

  const devices = live.devices;
  const gpus = devices?.gpus ?? noDevices;
  // 重新連線中的主機保留斷線前的資料; 連線中的主機於收到第一筆取樣後才顯示卡片.
  const showsCards =
    devices !== null &&
    gpus.length > 0 &&
    (host.state === "reconnecting" || (host.state === "connected" && live.has_sample));
  const collapsedGPUs = p.collapsedGPUs ?? noGPUs;
  const anyExpanded = gpus.some((g) => !collapsedGPUs.includes(g.id));
  const fields = useMemo(() => fieldsFor(p.curves), [p.curves]);
  const result = useCurveFeed(host.id, showsCards && anyExpanded && p.to > 0, p.from, p.to, chartWidth, fields);

  const data = useMemo(() => (result ? toChartData(result, t) : null), [result, t]);
  const seriesByGPU = useMemo(() => {
    const m = new Map<number, ReturnType<typeof gpuSeries>>();
    if (result) {
      for (const g of gpus) {
        m.set(g.id, gpuSeries(result, g.id, g, p.curves, t));
      }
    }
    return m;
  }, [result, gpus, p.curves, t]);

  if (!showsCards) {
    let note: string;
    if (host.state === "disconnected") {
      note = t("monitor.notConnected");
    } else if (host.state === "connecting") {
      note = t("monitor.connecting");
    } else if (devices && gpus.length === 0) {
      note = t("monitor.noGpu");
    } else {
      note = t("monitor.waiting");
    }
    return <div className="empty-note">{note}</div>;
  }

  return (
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
        collapsed={p.procsCollapsed}
        onToggle={onToggleProcs}
      />
    </>
  );
});
