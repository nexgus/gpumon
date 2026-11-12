import { memo, useCallback, useMemo, useState } from "react";
import type { Curve, Device, HostStatus, Live } from "../api/types";
import { createHoverStore } from "../chart/hoverStore";
import { fieldsFor, gpuSeries, toChartData } from "../chart/gpuSeries";
import { formatClock } from "../format";
import { useT } from "../i18n";
import { emptyLive, liveStore, setGPUCollapsed, setProcessesCollapsed, useSizeUnits } from "../state/app";
import { useStore } from "../state/store";
import { GpuRow } from "./GpuRow";
import { ProcessTable } from "./ProcessTable";
import { useLiveCurves } from "./useLiveCurves";

const noGPUs: number[] = [];
const noDevices: Device[] = [];
// 監看畫面的曲線圖共用的滑鼠狀態.
const hoverStore = createHoverStore();

export interface HostBlockProps {
  host: HostStatus;
  // 曲線圖的顯示範圍為 [to - span, to] (Unix 微秒); to 為 0 表示尚無取樣.
  span: number;
  to: number;
  curves: Curve[];
  collapsedGPUs: number[] | undefined;
  procsCollapsed: boolean;
  // onZoom 回報曲線圖上的滾輪與捏合縮放, 見 Chart 的 onZoom.
  onZoom: (factor: number, at: number) => void;
}

// HostBlock 為監看畫面中一台主機的內容: 每張 GPU 一張卡片, 最後為程序卡片. 沒有 GPU
// 卡片時只顯示一行說明; 重新連線中的主機照常顯示斷線前的讀數與曲線.
export const HostBlock = memo(function HostBlock(p: HostBlockProps) {
  const t = useT();
  const units = useSizeUnits();
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
  const result = useLiveCurves(host.id, showsCards && anyExpanded && p.to > 0, live.conn_start, p.to, p.span, chartWidth, fields);

  const data = useMemo(() => (result ? toChartData(result, t) : null), [result, t]);
  const seriesByGPU = useMemo(() => {
    const m = new Map<number, ReturnType<typeof gpuSeries>>();
    if (result) {
      for (const g of gpus) {
        m.set(g.id, gpuSeries(result, g.id, g, p.curves, units, t));
      }
    }
    return m;
  }, [result, gpus, p.curves, units, t]);

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
          from={p.to - p.span}
          to={p.to}
          data={data}
          series={seriesByGPU.get(g.id) ?? []}
          onWidth={onWidth}
          hoverStore={hoverStore}
          onZoom={p.onZoom}
        />
      ))}
      <ProcessTable
        hostID={host.id}
        gpus={gpus}
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
