// 監看畫面曲線的增量更新. 監看畫面的分桶對齊絕對時刻: 每個像素範圍的長度固定為
// bin 微秒, 第 k 個像素範圍為 [k * bin, (k + 1) * bin - 1]. 查詢範圍取顯示範圍所涉及的
// 完整像素範圍, 使右緣前進時各像素範圍的邊界不變, 新取樣只需併入最後一個像素範圍,
// 左側離開顯示範圍的像素範圍整個移除. 依此併入的結果與以同一組像素範圍重新查詢的結果
// 相同 (降採樣規則與無資料時段規則見 internal/history 的 QueryRange).
import type { Field, Gap, GPUCurves, GPUFileMax, QueryResult, SeriesData } from "../api/types";

// tailWidth 為查詢新取樣時的像素寬度. 新取樣多於此數時查詢結果改為降採樣, 呼叫端改為
// 重新查詢整段顯示範圍.
export const tailWidth = 32;

// Grid 為一組對齊絕對時刻的像素範圍: bin 為每個像素範圍的長度 (微秒), first 與 last
// 為第一個與最後一個像素範圍的編號.
export interface Grid {
  bin: number;
  first: number;
  last: number;
}

// gridFor 回傳顯示範圍 [to - span, to] 所涉及的像素範圍. 像素範圍的長度取 span / width
// 的上整數, 只隨顯示範圍長度與曲線圖寬度改變.
export function gridFor(to: number, span: number, width: number): Grid {
  const bin = Math.max(1, Math.ceil(span / Math.max(1, width)));
  return { bin, first: Math.floor((to - span) / bin), last: Math.floor(to / bin) };
}

// gridRange 回傳查詢 g 所用的範圍與像素寬度.
export function gridRange(g: Grid): { from: number; to: number; width: number } {
  return { from: g.first * g.bin, to: (g.last + 1) * g.bin - 1, width: g.last - g.first + 1 };
}

interface Series {
  avg: (number | null)[];
  min: (number | null)[];
  max: (number | null)[];
  sum: number[];
  count: number[];
}

// LiveState 為降採樣結果的可併入形式. 陣列皆以像素範圍 first 起算; breaks 為絕對編號.
export interface LiveState {
  grid: Grid;
  fields: Field[];
  ids: number[];
  samples: number;
  // last 為已併入的最後一筆取樣的時刻.
  last: number;
  counts: number[];
  series: Series[][];
  breaks: number[];
  gaps: Gap[];
  // fileMax 為最近一次查詢結果所附的整檔最大值, 每次併入新取樣時以該次查詢的結果取代.
  fileMax: GPUFileMax[] | null;
}

function num(v: number | null | undefined): number | null {
  return v === null || v === undefined ? null : v;
}

// fromResult 把以 stats 查詢 gridRange(grid) 所得的結果轉為可併入的狀態; 結果為原始
// 取樣點或缺少統計時回傳 null.
export function fromResult(res: QueryResult, grid: Grid, fields: Field[]): LiveState | null {
  const n = grid.last - grid.first + 1;
  if (!res.binned || !res.counts || res.counts.length !== n) {
    return null;
  }
  const series: Series[][] = [];
  for (const g of res.gpus ?? []) {
    const row: Series[] = [];
    for (const f of fields) {
      const d = g.fields[f];
      if (!d || !d.sum || !d.count || d.count.length !== n) {
        return null;
      }
      row.push({
        avg: d.avg.map(num),
        min: (d.min ?? []).map(num),
        max: (d.max ?? []).map(num),
        sum: d.sum.map((v) => v ?? 0),
        count: d.count.slice(),
      });
    }
    series.push(row);
  }
  return {
    grid: { ...grid },
    fields,
    ids: (res.gpus ?? []).map((g) => g.id),
    samples: res.samples,
    last: res.last,
    counts: res.counts.slice(),
    series,
    breaks: (res.breaks ?? []).map((i) => i + grid.first),
    gaps: (res.gaps ?? []).slice(),
    fileMax: res.file_max ?? null,
  };
}

// tailRange 回傳查詢 st 之後新取樣所用的範圍: 自已併入的最後一筆取樣之後到 next 的
// 最後一個像素範圍的結尾. 範圍為空時回傳 null.
export function tailRange(st: LiveState, next: Grid): { from: number; to: number } | null {
  const from = st.last + 1;
  const to = (next.last + 1) * next.bin - 1;
  return from <= to ? { from, to } : null;
}

const gapKey = (start: number, end: number) => `${start}:${end}`;

// merge 把 tail (以 tailRange 的範圍, 像素寬度 tailWidth, 不帶 stats 查詢的結果, null
// 表示範圍為空) 併入 st 並把像素範圍移至 next, 直接修改 st. 無法以併入取得與重新查詢
// 相同的結果時回傳 false, 呼叫端須重新查詢整段顯示範圍; 此時 st 已不可再用.
export function merge(st: LiveState, tail: QueryResult | null, next: Grid): boolean {
  const g = st.grid;
  if (next.bin !== g.bin || next.first < g.first || next.last < g.last) {
    return false;
  }
  if (tail && tail.binned) {
    return false;
  }
  const nf = st.fields.length;
  // tail 的各 GPU 對應至 st.series 的位置; tail 有新的 GPU 時, 重新查詢才能得到正確的
  // GPU 清單.
  const tailGPUs: { at: number; g: GPUCurves }[] = [];
  if (tail && tail.samples > 0) {
    for (const tg of tail.gpus ?? []) {
      const at = st.ids.indexOf(tg.id);
      if (at < 0) {
        return false;
      }
      tailGPUs.push({ at, g: tg });
    }
  }

  // 延伸至 next 的最後一個像素範圍.
  for (let k = g.last + 1; k <= next.last; k++) {
    st.counts.push(0);
    for (const row of st.series) {
      for (const s of row) {
        s.avg.push(null);
        s.min.push(null);
        s.max.push(null);
        s.sum.push(0);
        s.count.push(0);
      }
    }
  }
  g.last = next.last;

  if (tail && tail.samples > 0) {
    const keys = new Set(st.gaps.filter((x) => x.end > st.last).map((x) => gapKey(x.start, x.end)));
    for (const x of tail.gaps ?? []) {
      const k = gapKey(x.start, x.end);
      if (!keys.has(k)) {
        keys.add(k);
        st.gaps.push(x);
      }
    }
    let prev = st.last;
    let prevBin = Math.floor(prev / g.bin);
    const values: (SeriesData | null | undefined)[][] = tailGPUs.map(({ g: tg }) => st.fields.map((f) => tg.fields[f]));
    for (let i = 0; i < tail.times.length; i++) {
      const t = tail.times[i];
      const b = Math.floor(t / g.bin);
      if (t <= prev || b > g.last) {
        return false;
      }
      // 與重新查詢相同: 前一筆取樣在範圍內, 兩筆之間為無資料時段且位於相鄰的像素範圍時斷開.
      if (b === prevBin + 1 && prevBin >= next.first && keys.has(gapKey(prev, t))) {
        st.breaks.push(prevBin);
      }
      const k = b - g.first;
      st.counts[k]++;
      st.samples++;
      for (let u = 0; u < tailGPUs.length; u++) {
        const row = st.series[tailGPUs[u].at];
        for (let fi = 0; fi < nf; fi++) {
          const v = values[u][fi]?.avg[i];
          if (v === null || v === undefined) {
            continue;
          }
          const s = row[fi];
          if (s.count[k] === 0) {
            s.min[k] = v;
            s.max[k] = v;
          } else {
            s.min[k] = Math.min(s.min[k] as number, v);
            s.max[k] = Math.max(s.max[k] as number, v);
          }
          s.sum[k] += v;
          s.count[k]++;
          s.avg[k] = s.sum[k] / s.count[k];
        }
      }
      prev = t;
      prevBin = b;
    }
    st.last = prev;
  }

  if (tail?.file_max) {
    st.fileMax = tail.file_max;
  }

  // 移除左側離開顯示範圍的像素範圍.
  const drop = next.first - g.first;
  if (drop > 0) {
    for (let k = 0; k < drop; k++) {
      st.samples -= st.counts[k];
    }
    st.counts.splice(0, drop);
    for (const row of st.series) {
      for (const s of row) {
        s.avg.splice(0, drop);
        s.min.splice(0, drop);
        s.max.splice(0, drop);
        s.sum.splice(0, drop);
        s.count.splice(0, drop);
      }
    }
    g.first = next.first;
    st.breaks = st.breaks.filter((b) => b >= g.first);
    const from = g.first * g.bin;
    st.gaps = st.gaps.filter((x) => x.end >= from);
  }

  // 取樣筆數不多於像素寬度時, 重新查詢的結果為原始取樣點.
  return st.samples > g.last - g.first + 1;
}

// toResult 回傳 st 的查詢結果形式, 陣列皆為副本.
export function toResult(st: LiveState): QueryResult {
  const { from, to, width } = gridRange(st.grid);
  const times = new Array<number>(width);
  for (let k = 0; k < width; k++) {
    times[k] = from + k * st.grid.bin;
  }
  return {
    from,
    to,
    width,
    binned: true,
    samples: st.samples,
    last: st.last,
    times,
    breaks: st.breaks.map((b) => b - st.grid.first),
    gpus: st.ids.map((id, u) => {
      const fields: GPUCurves["fields"] = {};
      st.fields.forEach((f, fi) => {
        const s = st.series[u][fi];
        fields[f] = { avg: s.avg.slice(), min: s.min.slice(), max: s.max.slice() };
      });
      return { id, fields };
    }),
    gaps: st.gaps.slice(),
    file_max: st.fileMax,
  };
}
