// 把曲線查詢的結果換算為曲線圖 (chart/Chart.tsx) 的輸入. 監看畫面與歷史畫面共用.
import type { Curve, Device, Field, Gap, QueryResult, SeriesData, SeriesValues } from "../api/types";
import type { Translator } from "../i18n";
import { gib } from "../format";
import type { ChartData, ChartGap, ChartSeries } from "./Chart";

// allCurves 為曲線選單的順序, 亦為圖例與曲線的繪製順序.
export const allCurves: Curve[] = ["util", "mem", "temp", "fan", "power", "clock_gpu", "clock_mem"];

export const curveColor: Record<Curve, string> = {
  util: "--curve-util",
  mem: "--curve-mem",
  temp: "--curve-temp",
  fan: "--curve-fan",
  power: "--curve-power",
  clock_gpu: "--curve-clk-gpu",
  clock_mem: "--curve-clk-mem",
};

export const curveField: Record<Curve, Field> = {
  util: "util",
  mem: "mem_used",
  temp: "temp",
  fan: "fan",
  power: "power_w",
  clock_gpu: "clock_gpu",
  clock_mem: "clock_mem",
};

// fieldsFor 回傳畫出 curves 所需查詢的欄位, 依 allCurves 的順序.
export function fieldsFor(curves: Curve[]): Field[] {
  return allCurves.filter((c) => curves.includes(c)).map((c) => curveField[c]);
}

// gapDetail 回傳無資料時段的原因文字, 規則見規格 6.2 的 "無資料時段".
export function gapDetail(g: Gap, t: Translator): string {
  switch (g.reason) {
    case "error":
    case "disconnect":
      return g.message || t("gap.unknown");
    case "not_connected":
      return t("gap.notConnected");
  }
  return t("gap.unknown");
}

// toChartData 取出查詢結果中與 GPU 無關的部分. extraGaps 為呼叫端另行標示的無資料
// 時段, 例如歷史畫面中記錄範圍以外的時段.
export function toChartData(res: QueryResult, t: Translator, extraGaps: ChartGap[] = []): ChartData {
  return {
    binned: res.binned,
    binSpan: res.binned && res.width > 0 ? (res.to - res.from) / res.width : 0,
    times: res.times ?? [],
    breaks: res.breaks ?? [],
    gaps: [...(res.gaps ?? []).map((g) => ({ start: g.start, end: g.end, detail: gapDetail(g, t) })), ...extraGaps],
  };
}

function maxOf(...arrays: (SeriesValues | undefined)[]): number | null {
  let m: number | null = null;
  for (const a of arrays) {
    if (!a) {
      continue;
    }
    for (const v of a) {
      if (v !== null && v !== undefined && (m === null || v > m)) {
        m = v;
      }
    }
  }
  return m;
}

function hasValue(d: SeriesData | null | undefined): boolean {
  return !!d && d.avg.some((v) => v !== null && v !== undefined);
}

function scaled(a: SeriesValues | undefined, full: number): SeriesValues | undefined {
  if (!a) {
    return undefined;
  }
  return a.map((v) => (v === null || v === undefined ? null : (v / full) * 100));
}

function round(v: number | null | undefined): number | null {
  return v === null || v === undefined ? null : Math.round(v);
}

// gpuSeries 回傳一張 GPU 的曲線. 使用率與記憶體佔比以 0 至 100 為縱軸; 加選的曲線依
// 規格 6.2 的滿刻度規則換算: 溫度以 100 度, 風扇轉速以 100%, 功耗與時脈以 devices
// 訊息的上限為滿刻度; 顯示範圍內的值超過滿刻度, 或取不到上限時, 改以顯示範圍內的
// 最大值為滿刻度, 圖例標示該值. 取不到記憶體總量時不畫記憶體佔比; 加選的曲線於顯示
// 範圍內完全沒有值時不畫.
export function gpuSeries(
  res: QueryResult,
  gpuID: number,
  device: Device | undefined,
  curves: Curve[],
  t: Translator,
): ChartSeries[] {
  const g = res.gpus?.find((x) => x.id === gpuID);
  const out: ChartSeries[] = [];
  const na = t("value.na");
  for (const c of allCurves) {
    if (!curves.includes(c)) {
      continue;
    }
    const d = g?.fields[curveField[c]] ?? null;
    const avg = d?.avg ?? res.times.map(() => null);
    const base = { key: c, color: curveColor[c], label: t(`legend.${c}`) };
    if (c === "util") {
      out.push({
        ...base,
        avg,
        min: d?.min,
        max: d?.max,
        format: (i) => {
          const v = round(avg[i]);
          return v === null ? na : t("unit.percent", { value: v });
        },
      });
      continue;
    }
    if (c === "mem") {
      const total = device?.mem_total;
      if (!total) {
        continue;
      }
      const pct = scaled(avg, total) as SeriesValues;
      out.push({
        ...base,
        avg: pct,
        min: scaled(d?.min, total),
        max: scaled(d?.max, total),
        format: (i) => {
          const v = avg[i];
          if (v === null || v === undefined) {
            return na;
          }
          return t("value.memPercent", {
            value: t("unit.gib", { value: gib(v) }),
            percent: t("unit.percent", { value: Math.round((v / total) * 100) }),
          });
        },
      });
      continue;
    }
    if (!hasValue(d)) {
      continue;
    }
    const unitKey = c === "temp" ? "unit.celsius" : c === "fan" ? "unit.percent" : c === "power" ? "unit.watt" : "unit.mhz";
    const limit =
      c === "temp" || c === "fan"
        ? 100
        : c === "power"
          ? device?.power_max_w
          : c === "clock_gpu"
            ? device?.clock_gpu_max
            : device?.clock_mem_max;
    const peak = maxOf(d?.max, avg) ?? 0;
    const fallback = !limit || peak > limit;
    const full = Math.max(1, fallback ? Math.ceil(peak) : (limit as number));
    const fullText = t(unitKey, { value: full });
    out.push({
      ...base,
      scaleLabel: fallback ? t("legend.maxInView", { value: fullText }) : fullText,
      avg: scaled(avg, full) as SeriesValues,
      min: scaled(d?.min, full),
      max: scaled(d?.max, full),
      format: (i) => {
        const v = round(avg[i]);
        return v === null ? na : t(unitKey, { value: v });
      },
    });
  }
  return out;
}
