import type { Field, LiveGPU, QueryResult, SeriesData } from "../api/types";

// historyFields 為歷史畫面查詢的欄位. 文字讀數顯示全部數值, 與曲線選單的選擇無關,
// 故一律查詢讀數用到的七個欄位.
export const historyFields: Field[] = ["util", "mem_used", "temp", "power_w", "clock_gpu", "clock_mem", "fan"];

function pointTime(res: QueryResult, i: number): number {
  if (res.binned && res.width > 0) {
    return res.times[i] + (res.to - res.from) / res.width / 2;
  }
  return res.times[i];
}

function rounded(d: SeriesData | null | undefined, i: number): number | null {
  const v = d?.avg[i];
  return v === null || v === undefined ? null : Math.round(v);
}

// liveAt 回傳某張 GPU 在時刻 t (Unix 微秒) 的數值, 供文字讀數使用. 取最接近 t 且有
// 任一欄位有值的點; 距離超過約 16 個像素, 或沒有任何點時回傳 undefined, 讀數顯示 N/A.
export function liveAt(res: QueryResult | null, gpuID: number, t: number): LiveGPU | undefined {
  const g = res?.gpus?.find((x) => x.id === gpuID);
  if (!res || !g) {
    return undefined;
  }
  const n = res.times.length;
  if (n === 0 || res.width <= 0) {
    return undefined;
  }
  let lo = 0;
  let hi = n - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (pointTime(res, mid) < t) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  const has = (i: number) => historyFields.some((f) => g.fields[f]?.avg[i] != null);
  let best = -1;
  let bestDist = Infinity;
  for (let i = Math.max(0, lo - 4); i <= Math.min(n - 1, lo + 4); i++) {
    const dist = Math.abs(pointTime(res, i) - t);
    if (has(i) && dist < bestDist) {
      best = i;
      bestDist = dist;
    }
  }
  const maxDist = ((res.to - res.from) / res.width) * 16;
  if (best < 0 || bestDist > maxDist) {
    return undefined;
  }
  const f = g.fields;
  return {
    id: gpuID,
    util: rounded(f.util, best),
    mem_used: rounded(f.mem_used, best),
    temp: rounded(f.temp, best),
    power_w: rounded(f.power_w, best),
    clock_gpu: rounded(f.clock_gpu, best),
    clock_mem: rounded(f.clock_mem, best),
    fan: rounded(f.fan, best),
    enc: null,
    dec: null,
    util_computed: false,
  };
}
