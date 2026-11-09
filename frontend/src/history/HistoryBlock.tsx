import { memo, useCallback, useMemo, useState } from "react";
import { backend } from "../api/backend";
import type { Curve, Device, Field, LiveProcess, QueryResult, RecordingInfo } from "../api/types";
import type { ChartGap } from "../chart/Chart";
import { gpuSeries, toChartData } from "../chart/gpuSeries";
import { formatClock, formatDateRange } from "../format";
import { useT } from "../i18n";
import { setGPUCollapsed, setProcessesCollapsed } from "../state/app";
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
  // from 與 to 為所有分組共用的顯示範圍 (Unix 微秒).
  from: number;
  to: number;
  curves: Curve[];
  collapsedGPUs: number[] | undefined;
  procsCollapsed: boolean;
}

// HistoryBlock 為歷史畫面中一個記錄檔的分組: 一行分組標題, 其下為每張 GPU 的卡片與
// 程序卡片. 文字讀數與程序卡片顯示滑鼠在本分組曲線圖上所指的時刻, 滑鼠不在曲線圖上
// 時顯示顯示範圍右緣的時刻.
export const HistoryBlock = memo(function HistoryBlock({
  info,
  from,
  to,
  curves,
  collapsedGPUs,
  procsCollapsed,
}: HistoryBlockProps) {
  const t = useT();
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
        m.set(g.id, gpuSeries(result, g.id, g, curves, t));
      }
    }
    return m;
  }, [result, gpus, curves, t]);

  const readAt = hover ?? to;
  const procs = useProcessesAt(info.path, readAt);

  return (
    <div className="history-group">
      <header className="history-group-head">
        <span className="history-group-title">{info.devices?.hostname || info.host}</span>
        <span className="history-group-detail">{info.host}</span>
        <span className="flex1" />
        <span className="history-group-detail">{formatDateRange(info.start, info.end)}</span>
      </header>
      {gpus.length === 0 && <div className="card card-note">{t("history.noGpu")}</div>}
      {gpus.map((g) => (
        <GpuRow
          key={g.id}
          device={g}
          gpu={liveAt(result, g.id, readAt)}
          collapsed={collapsed.includes(g.id)}
          onToggle={onToggle}
          from={from}
          to={to}
          data={data}
          series={seriesByGPU.get(g.id) ?? []}
          onWidth={onWidth}
          onHover={setHover}
        />
      ))}
      {gpus.length > 0 && (
        <ProcessTable
          title={
            procs === undefined
              ? t("monitor.processes")
              : procs === null
                ? t("history.noProcessRecord", { time: formatClock(readAt) })
                : t("history.processesAt", { time: formatClock(procs.time) })
          }
          procs={procs?.procs ?? noProcs}
          emptyText={t("history.noProcesses")}
          collapsed={procsCollapsed}
          onToggle={onToggleProcs}
        />
      )}
    </div>
  );
});
