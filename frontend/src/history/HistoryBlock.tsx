import { memo, useCallback, useMemo, useState } from "react";
import { backend } from "../api/backend";
import type { Curve, Device, Field, LiveProcess, QueryResult, RecordingInfo } from "../api/types";
import type { ChartEdge, ChartGap } from "../chart/Chart";
import { gpuSeries, toChartData } from "../chart/gpuSeries";
import { formatClock } from "../format";
import { useT } from "../i18n";
import { setGPUCollapsed, setProcessesCollapsed, useSizeUnits } from "../state/app";
import { GpuRow } from "../monitor/GpuRow";
import { ProcessTable } from "../monitor/ProcessTable";
import { useCurveFeed } from "../monitor/useCurveFeed";
import { historyFields, liveAt } from "./readout";
import { useProcessesAt } from "./useProcessesAt";

const noGPUs: number[] = [];
const noDevices: Device[] = [];
const noProcs: LiveProcess[] = [];

function queryRecording(path: string, from: number, to: number, width: number, fields: Field[]): Promise<QueryResult | null> {
  return backend.queryRecording(path, from, to, width, fields);
}

export interface HistoryBlockProps {
  info: RecordingInfo;
  // from 與 to 為顯示範圍 (Unix 微秒).
  from: number;
  to: number;
  curves: Curve[];
  collapsedGPUs: number[] | undefined;
  procsCollapsed: boolean;
  // pinned 為歷史畫面的固定時刻 (Unix 微秒), null 為未固定. onPin 於點選曲線圖時
  // 回報該處的時刻.
  pinned: number | null;
  onPin: (t: number) => void;
  // edge 為曲線圖的橫向捲動介面. onZoom 回報曲線圖上的滾輪與捏合縮放.
  edge: ChartEdge;
  onZoom: (factor: number, at: number) => void;
}

// HistoryBlock 為歷史畫面中記錄檔的內容: 每張 GPU 的卡片與程序卡片. 文字讀數與程序
// 卡片顯示滑鼠在曲線圖上所指的時刻; 滑鼠不在曲線圖上時, 有固定時刻則顯示固定時刻,
// 否則顯示顯示範圍右緣的時刻.
export const HistoryBlock = memo(function HistoryBlock({
  info,
  from,
  to,
  curves,
  collapsedGPUs,
  procsCollapsed,
  pinned,
  onPin,
  edge,
  onZoom,
}: HistoryBlockProps) {
  const t = useT();
  const units = useSizeUnits();
  const gpus = info.devices?.gpus ?? noDevices;
  const collapsed = collapsedGPUs ?? noGPUs;
  const anyExpanded = gpus.some((g) => !collapsed.includes(g.id));
  const [chartWidth, setChartWidth] = useState(0);
  const [hover, setHover] = useState<number | null>(null);
  const onWidth = useCallback((w: number) => setChartWidth(w), []);
  const onToggle = useCallback(
    (gpuID: number, c: boolean) => void setGPUCollapsed(info.host, gpuID, c),
    [info.host],
  );
  const onToggleProcs = useCallback((c: boolean) => void setProcessesCollapsed(info.host, c), [info.host]);

  const result = useCurveFeed(info.path, anyExpanded, from, to, chartWidth, historyFields, queryRecording);

  // 固定時刻離開顯示範圍時, 文字讀數另查詢以固定時刻為中心的同寬範圍.
  const pinOutside = pinned !== null && (pinned < from || pinned > to);
  const half = (to - from) / 2;
  const pinResult = useCurveFeed(
    info.path,
    anyExpanded && pinOutside,
    pinOutside ? pinned - half : from,
    pinOutside ? pinned + half : to,
    chartWidth,
    historyFields,
    queryRecording,
  );

  // 記錄檔起訖之外的時段沒有資料, 以無資料時段標示.
  const outside = useMemo((): ChartGap[] => {
    const out: ChartGap[] = [];
    if (info.start > from) {
      out.push({
        start: from,
        end: info.start,
        title: t("gap.before", { time: formatClock(info.start) }),
        detail: t("gap.notConnected"),
      });
    }
    if (info.end < to) {
      out.push({
        start: info.end,
        end: to,
        title: t("gap.after", { time: formatClock(info.end) }),
        detail: t("gap.notConnected"),
      });
    }
    return out;
  }, [info.start, info.end, from, to, t]);

  const data = useMemo(() => (result ? toChartData(result, t, outside) : null), [result, t, outside]);
  const seriesByGPU = useMemo(() => {
    const m = new Map<number, ReturnType<typeof gpuSeries>>();
    if (result) {
      for (const g of gpus) {
        m.set(g.id, gpuSeries(result, g.id, g, curves, units, t));
      }
    }
    return m;
  }, [result, gpus, curves, units, t]);

  const readAt = hover ?? pinned ?? to;
  const readResult = hover === null && pinOutside ? pinResult : result;
  const pinHint = t(pinned === null ? "history.pinHint" : "history.repinHint");
  const procs = useProcessesAt(info.path, readAt);

  return (
    <>
      {gpus.length === 0 && <div className="card card-note">{t("history.noGpu")}</div>}
      {gpus.map((g) => (
        <GpuRow
          key={g.id}
          device={g}
          gpu={liveAt(readResult, g.id, readAt)}
          collapsed={collapsed.includes(g.id)}
          onToggle={onToggle}
          from={from}
          to={to}
          data={data}
          series={seriesByGPU.get(g.id) ?? []}
          onWidth={onWidth}
          onHover={setHover}
          pinned={pinned}
          onPin={onPin}
          pinHint={pinHint}
          edge={edge}
          onZoom={onZoom}
        />
      ))}
      {gpus.length > 0 && (
        <ProcessTable
          hostID={info.host}
          gpus={gpus}
          title={
            procs === undefined
              ? t("monitor.processes")
              : procs === null
                ? t("history.noProcessRecord", { time: formatClock(readAt) })
                : t("history.processesAt", { time: formatClock(procs.time) })
          }
          procs={procs?.procs ?? noProcs}
          emptyText={t("history.noProcesses")}
          missingText={t("history.processAbsent")}
          collapsed={procsCollapsed}
          onToggle={onToggleProcs}
        />
      )}
    </>
  );
});
