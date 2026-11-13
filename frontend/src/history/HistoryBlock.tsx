import { memo, useCallback, useMemo, useRef, useState } from "react";
import { backend } from "../api/backend";
import type { Curve, Device, Field, FieldMax, LiveGPU, LiveProcess, QueryResult, RecordingInfo, RecordingMaxima } from "../api/types";
import type { ChartEdge, ChartGap, ChartLegendExtra, ChartMarks } from "../chart/Chart";
import { createHoverStore } from "../chart/hoverStore";
import { gpuSeries, toChartData, type FileMaxValues } from "../chart/gpuSeries";
import { formatClock } from "../format";
import { useT } from "../i18n";
import { setGPUCollapsed, setMaximaCollapsed, setProcessesCollapsed, useSizeUnits, type MaxSelection } from "../state/app";
import { GpuRow } from "../monitor/GpuRow";
import { ProcessTable } from "../monitor/ProcessTable";
import { useStore } from "../state/store";
import { useCurveFeed } from "../monitor/useCurveFeed";
import { findMax, MaximaCard, maxColumns } from "./MaximaCard";
import { historyFields, liveAt, sampleGPU } from "./readout";
import { useMaxima } from "./useMaxima";
import { useProcessesAt } from "./useProcessesAt";
import { useSampleAt } from "./useSampleAt";

const noGPUs: number[] = [];
const noDevices: Device[] = [];
const noProcs: LiveProcess[] = [];
// 歷史畫面的曲線圖共用的滑鼠狀態.
const hoverStore = createHoverStore();

// fileMaxOf 取出一張 GPU 各欄位的整檔最大值, 供 gpuSeries 決定滿刻度; undefined 與 null
// 沿用 useMaxima 的意義, 分別為查詢中與查詢失敗.
function fileMaxOf(maxima: RecordingMaxima | null | undefined, gpu: number): FileMaxValues | null | undefined {
  if (!maxima) {
    return maxima;
  }
  const out: FileMaxValues = {};
  const fields = maxima.gpus.find((g) => g.id === gpu)?.fields ?? {};
  for (const [f, m] of Object.entries(fields) as [Field, FieldMax][]) {
    out[f] = m.value;
  }
  return out;
}

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
  maximaCollapsed: boolean;
  // pinned 為歷史畫面的固定時刻 (Unix 微秒), null 為未固定. onPin 於點選曲線圖時
  // 回報該處的時刻.
  pinned: number | null;
  onPin: (t: number) => void;
  // maxSel 為最大值卡片的選取; onMaxPick 於點選最大值或切換出現處時回報新的選取與該處的
  // 時刻. timeText 回傳時刻的文字, 格式同固定時刻標籤.
  maxSel: MaxSelection | null;
  onMaxPick: (sel: MaxSelection, t: number) => void;
  timeText: (us: number) => string;
  // edge 為曲線圖的橫向捲動介面. onZoom 回報曲線圖上的滾輪與捏合縮放.
  edge: ChartEdge;
  onZoom: (factor: number, at: number) => void;
}

// HistoryBlock 為歷史畫面中記錄檔的內容: 最大值卡片, 每張 GPU 的卡片與程序卡片. 文字
// 讀數與程序卡片顯示滑鼠在曲線圖上所指的時刻; 滑鼠不在曲線圖上時, 有固定時刻則顯示固定
// 時刻, 否則顯示顯示範圍右緣的時刻. 固定時刻的文字讀數為當筆取樣的實際數值, 不取曲線
// 查詢的平均值.
export const HistoryBlock = memo(function HistoryBlock({
  info,
  from,
  to,
  curves,
  collapsedGPUs,
  procsCollapsed,
  maximaCollapsed,
  pinned,
  onPin,
  maxSel,
  onMaxPick,
  timeText,
  edge,
  onZoom,
}: HistoryBlockProps) {
  const t = useT();
  const units = useSizeUnits();
  const gpus = info.devices?.gpus ?? noDevices;
  const collapsed = collapsedGPUs ?? noGPUs;
  const anyExpanded = gpus.some((g) => !collapsed.includes(g.id));
  const [chartWidth, setChartWidth] = useState(0);
  const hover = useStore(hoverStore, (h) => h.t);
  const onWidth = useCallback((w: number) => setChartWidth(w), []);
  const onToggle = useCallback(
    (gpuID: number, c: boolean) => void setGPUCollapsed(info.host, gpuID, c),
    [info.host],
  );
  const onToggleProcs = useCallback((c: boolean) => void setProcessesCollapsed(info.host, c), [info.host]);
  const onToggleMaxima = useCallback((c: boolean) => void setMaximaCollapsed(info.host, c), [info.host]);
  const maxima = useMaxima(info.path);

  const result = useCurveFeed(info.path, anyExpanded, from, to, chartWidth, historyFields, queryRecording);

  const sample = useSampleAt(info.path, pinned);

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
        m.set(g.id, gpuSeries(result, g.id, g, curves, units, t, fileMaxOf(maxima, g.id)));
      }
    }
    return m;
  }, [result, gpus, curves, units, t, maxima]);

  // 選取最大值時, 該 GPU 的曲線圖頂端標出該欄位的所有出現處, 圖例最後加一項說明.
  const selMax = maxSel ? findMax(maxima, maxSel.gpu, maxSel.field) : undefined;
  const marks = useMemo((): ChartMarks | null => (selMax ? { runs: selMax.runs, color: "--text" } : null), [selMax]);
  const selLegend = maxSel ? maxColumns.find((c) => c.field === maxSel.field)?.legend : undefined;
  const markLegend = useMemo(
    (): ChartLegendExtra | null =>
      selMax && selLegend ? { key: "max-marks", label: t("history.maxMarks", { name: t(selLegend) }), color: "--text" } : null,
    [selMax, selLegend, t],
  );

  // 有固定時刻且滑鼠不在曲線圖上時, 讀數取固定時刻的取樣; 查得之前維持前一個畫面的讀數.
  const readAt = hover ?? pinned ?? to;
  const fromSample = hover === null && pinned !== null;
  const sampleReady = sample !== undefined && sample.path === info.path && sample.t === pinned;
  const shown = useRef(new Map<number, LiveGPU | undefined>());
  const readout = (gpuID: number): LiveGPU | undefined => {
    let v: LiveGPU | undefined;
    if (!fromSample) {
      v = liveAt(result, gpuID, readAt);
    } else if (sampleReady && sample) {
      v = sampleGPU(sample.sample, gpuID);
    } else {
      v = shown.current.get(gpuID);
    }
    shown.current.set(gpuID, v);
    return v;
  };
  const pinHint = t(pinned === null ? "history.pinHint" : "history.repinHint");
  const procs = useProcessesAt(info.path, readAt);

  return (
    <>
      {gpus.length === 0 && <div className="card card-note">{t("history.noGpu")}</div>}
      {gpus.length > 0 && (
        <MaximaCard
          gpus={gpus}
          maxima={maxima}
          sel={maxSel}
          collapsed={maximaCollapsed}
          onToggle={onToggleMaxima}
          onPick={onMaxPick}
          timeText={timeText}
        />
      )}
      {gpus.map((g) => (
        <GpuRow
          key={g.id}
          device={g}
          gpu={readout(g.id)}
          collapsed={collapsed.includes(g.id)}
          onToggle={onToggle}
          from={from}
          to={to}
          data={data}
          series={seriesByGPU.get(g.id) ?? []}
          onWidth={onWidth}
          hoverStore={hoverStore}
          pinned={pinned}
          onPin={onPin}
          pinHint={pinHint}
          edge={edge}
          onZoom={onZoom}
          marks={maxSel?.gpu === g.id ? marks : null}
          markLegend={maxSel?.gpu === g.id ? markLegend : null}
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
