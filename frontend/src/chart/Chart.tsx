// 可重複使用的曲線圖. 以 canvas 繪製, 縱軸固定為 0 至 100, 橫軸為 [from, to] 的
// 時刻 (Unix 微秒). 元件只負責繪製與滑鼠懸停的讀數, 不查詢資料, 也不認得監看畫面
// 或歷史畫面; 資料的換算見 chart/gpuSeries.ts.
import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { onThemeChange } from "../theme";
import { formatClock, formatClockShort } from "../format";
import { isMac } from "../platform";

export interface ChartSeries {
  key: string;
  // color 為曲線顏色的 CSS 變數名稱, 例如 "--curve-util".
  color: string;
  // label 為圖例與懸停讀數的名稱; scaleLabel 為圖例中滿刻度的說明, 可省略.
  label: string;
  scaleLabel?: string;
  // avg 為已換算至 0 至 100 的平均值, min 與 max 為降採樣時的最小與最大值, 長度皆
  // 與 ChartData.times 相同; 留空的位置為 null.
  avg: (number | null)[];
  min?: (number | null)[];
  max?: (number | null)[];
  // format 回傳第 i 點的實際數值與單位, 用於懸停讀數.
  format: (i: number) => string;
}

export interface ChartGap {
  start: number;
  end: number;
  // detail 為懸停時顯示於起訖時刻之下的原因.
  detail: string;
  // title 省略時懸停讀數的第一行為 gapTitle 組出的 "No data 起 - 訖"; 歷史畫面於
  // 記錄檔起訖之外的時段傳入自己的說明.
  title?: string;
}

export interface ChartData {
  // binned 為真時, times[i] 為第 i 個像素範圍的起點, 每個像素範圍的長度為 binSpan;
  // 為假時 times 為每筆取樣的時刻.
  binned: boolean;
  binSpan: number;
  times: number[];
  // breaks 的元素 i 表示第 i 點與第 i+1 點之間須斷開.
  breaks: number[];
  gaps: ChartGap[];
}

export interface ChartProps {
  from: number;
  to: number;
  data: ChartData | null;
  series: ChartSeries[];
  ariaLabel: string;
  // gapTitle 回傳無資料時段懸停讀數的第一行, 例如 "No data 14:29:44 - 14:30:08".
  gapTitle: (start: string, end: string) => string;
  // onWidth 回報曲線圖繪圖區的實際寬度 (CSS 像素), 供呼叫端決定查詢的像素寬度.
  onWidth?: (width: number) => void;
  // onHover 回報滑鼠所指的時刻; 滑鼠離開時為 null.
  onHover?: (t: number | null) => void;
  // pinned 為固定時刻, 位於 [from, to] 內時畫一條固定線. onPin 於點選繪圖區時回報該處
  // 的時刻. pinHint 為數值提示最下方的操作說明. 三者皆省略時 (監看畫面) 不啟用.
  pinned?: number | null;
  onPin?: (t: number) => void;
  pinHint?: string;
  // edge 於歷史畫面傳入, 啟用邊緣提示, 邊緣自動捲動與橫向滾動; 省略時 (監看畫面) 不啟用.
  edge?: ChartEdge;
  // onZoom 回報繪圖區上按住 Ctrl (macOS 另可按住 ⌘) 的滾輪或觸控板捏合. factor 為顯示範圍
  // 長度的倍數, 大於 1 為加長; at 為游標位置佔繪圖區寬度的比例. 省略時不攔截這些操作.
  onZoom?: (factor: number, at: number) => void;
}

// ChartEdge 為曲線圖的橫向捲動介面. canLeft 與 canRight 表示該側還有資料可捲動.
// active 為正在自動捲動的方向 (-1 往較早, 1 往較晚, 0 為未捲動). onEdge 於游標在邊緣區
// 停留夠久時回報方向, 離開邊緣區時回報 0. onPan 回報橫向滾動的位移, 單位為繪圖區寬度
// 的倍數, 正值往較晚的時間.
// scrollable 表示捲動範圍長於顯示範圍, 才能按住拖曳平移. flinging 表示慣性捲動進行中.
// onHold 回報按住滑鼠 (平移中或尚未超過門檻) 的開始與結束. onStop 停止慣性捲動, 回傳
// 先前是否正在滑動. onDrag 將顯示範圍左緣設為指定時刻 (Unix 微秒, 超出範圍時限制於
// 捲動範圍內). onFling 於平移放開時回報起始速度 (手的水平速度, 像素/秒) 與繪圖區寬度.
export interface ChartEdge {
  canLeft: boolean;
  canRight: boolean;
  active: number;
  scrollable: boolean;
  flinging: boolean;
  onEdge: (dir: number) => void;
  onPan: (frac: number) => void;
  onHold: (held: boolean) => void;
  onStop: () => boolean;
  onDrag: (left: number) => void;
  onFling: (vx: number, width: number) => void;
}

const TICKS = 6;
const EDGE_ZONE = 24;
const EDGE_DWELL_MS = 300;
// 按下後移動超過此距離 (像素) 才視為拖曳.
const DRAG_THRESHOLD = 4;
// 放開前取樣速度的時間窗 (毫秒), 與低於此速度 (像素/秒) 不滑動的門檻.
const VELOCITY_WINDOW_MS = 100;
const FLING_MIN_SPEED = 300;
// 縮放時每 ZOOM_WHEEL_UNIT 的滾動量 (像素) 使長度乘以或除以 ZOOM_WHEEL_FACTOR, 不足時依比例
// 連續變化. 滾動量以行為單位時每行以 WHEEL_LINE 像素計.
const ZOOM_WHEEL_UNIT = 100;
const ZOOM_WHEEL_FACTOR = 1.25;
const WHEEL_LINE = 16;

// WebKitGesture 為 macOS WKWebView 的觸控板捏合事件, scale 為兩指距離相對於開始時的比例.
interface WebKitGesture extends UIEvent {
  scale: number;
  clientX?: number;
}

interface Gesture {
  x0: number;
  y0: number;
  from0: number;
  span0: number;
  pannable: boolean;
  panning: boolean;
  samples: { t: number; x: number }[];
}

function cssVar(el: Element, name: string): string {
  return getComputedStyle(el).getPropertyValue(name).trim();
}

interface Hover {
  x: number;
  lines: ReactNode;
  line: boolean;
}

function isBreak(breaks: Set<number>, i: number): boolean {
  return breaks.has(i);
}

// pointTime 回傳第 i 點於橫軸上的時刻; 降採樣時取像素範圍的中點.
function pointTime(d: ChartData, i: number): number {
  return d.binned ? d.times[i] + d.binSpan / 2 : d.times[i];
}

function draw(
  canvas: HTMLCanvasElement,
  width: number,
  height: number,
  from: number,
  to: number,
  data: ChartData | null,
  series: ChartSeries[],
): void {
  const dpr = window.devicePixelRatio || 1;
  const w = Math.max(1, Math.round(width * dpr));
  const h = Math.max(1, Math.round(height * dpr));
  if (canvas.width !== w || canvas.height !== h) {
    canvas.width = w;
    canvas.height = h;
  }
  const ctx = canvas.getContext("2d");
  if (!ctx) {
    return;
  }
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, width, height);
  const span = Math.max(1, to - from);
  const pad = 1.5;
  const xOf = (t: number) => ((t - from) / span) * width;
  const yOf = (v: number) => pad + (1 - v / 100) * (height - 2 * pad);

  ctx.strokeStyle = cssVar(canvas, "--grid");
  ctx.lineWidth = 1;
  for (const v of [25, 50, 75]) {
    const y = Math.round(yOf(v)) + 0.5;
    ctx.beginPath();
    ctx.moveTo(0, y);
    ctx.lineTo(width, y);
    ctx.stroke();
  }
  if (!data) {
    return;
  }

  ctx.fillStyle = cssVar(canvas, "--gap-fill");
  for (const g of data.gaps) {
    const x0 = Math.max(0, xOf(g.start));
    const x1 = Math.min(width, xOf(g.end));
    if (x1 > x0) {
      ctx.fillRect(x0, 0, x1 - x0, height);
    }
  }

  const n = data.times.length;
  const breaks = new Set(data.breaks);
  const xs = new Float64Array(n);
  for (let i = 0; i < n; i++) {
    xs[i] = xOf(pointTime(data, i));
  }
  const bandOpacity = Number(cssVar(canvas, "--band-opacity")) || 0.16;

  // 連續的一段為相鄰皆有值且其間未斷開的點; 曲線與淡色帶皆以段為單位繪製.
  const segments = (vals: (number | null)[]): [number, number][] => {
    const out: [number, number][] = [];
    let start = -1;
    for (let i = 0; i < n; i++) {
      const ok = vals[i] !== null && vals[i] !== undefined;
      if (ok && start < 0) {
        start = i;
      }
      if (start >= 0 && (!ok || i === n - 1 || isBreak(breaks, i))) {
        const end = ok ? i : i - 1;
        out.push([start, end]);
        start = -1;
      }
    }
    return out;
  };

  for (const s of series) {
    const color = cssVar(canvas, s.color);
    if (data.binned && s.min && s.max) {
      const lo = s.min;
      const hi = s.max;
      ctx.globalAlpha = bandOpacity;
      ctx.fillStyle = color;
      for (const [a, b] of segments(s.avg)) {
        ctx.beginPath();
        for (let i = a; i <= b; i++) {
          const v = hi[i] ?? s.avg[i] ?? 0;
          if (i === a) {
            ctx.moveTo(xs[i] - (a === b ? 0.75 : 0), yOf(v));
          } else {
            ctx.lineTo(xs[i], yOf(v));
          }
        }
        if (a === b) {
          ctx.lineTo(xs[a] + 0.75, yOf(hi[a] ?? s.avg[a] ?? 0));
          ctx.lineTo(xs[a] + 0.75, yOf(lo[a] ?? s.avg[a] ?? 0));
        }
        for (let i = b; i >= a; i--) {
          const v = lo[i] ?? s.avg[i] ?? 0;
          ctx.lineTo(xs[i] - (a === b && i === a ? 0.75 : 0), yOf(v));
        }
        ctx.closePath();
        ctx.fill();
      }
      ctx.globalAlpha = 1;
    }
    ctx.strokeStyle = color;
    ctx.fillStyle = color;
    ctx.lineWidth = 1.5;
    ctx.lineJoin = "round";
    for (const [a, b] of segments(s.avg)) {
      if (a === b) {
        ctx.beginPath();
        ctx.arc(xs[a], yOf(s.avg[a] as number), 1.25, 0, Math.PI * 2);
        ctx.fill();
        continue;
      }
      ctx.beginPath();
      ctx.moveTo(xs[a], yOf(s.avg[a] as number));
      for (let i = a + 1; i <= b; i++) {
        ctx.lineTo(xs[i], yOf(s.avg[i] as number));
      }
      ctx.stroke();
    }
  }
}

// nearestIndex 回傳最接近時刻 t 且有任一曲線有值的點, 距離超過 maxDist 時回傳 -1.
function nearestIndex(d: ChartData, series: ChartSeries[], t: number, maxDist: number): number {
  const n = d.times.length;
  if (n === 0) {
    return -1;
  }
  let lo = 0;
  let hi = n - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (pointTime(d, mid) < t) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  const has = (i: number) => series.some((s) => s.avg[i] !== null && s.avg[i] !== undefined);
  let best = -1;
  let bestDist = Infinity;
  for (let i = Math.max(0, lo - 4); i <= Math.min(n - 1, lo + 4); i++) {
    const dist = Math.abs(pointTime(d, i) - t);
    if (has(i) && dist < bestDist) {
      best = i;
      bestDist = dist;
    }
  }
  return bestDist <= maxDist ? best : -1;
}

function ticks(from: number, to: number): string[] {
  const span = to - from;
  const fmt = span >= 3 * 3600e6 ? formatClockShort : formatClock;
  const out: string[] = [];
  for (let i = 0; i < TICKS; i++) {
    out.push(fmt(from + (span * i) / (TICKS - 1)));
  }
  return out;
}

// ChartLegend 為曲線圖的圖例, 含各曲線的名稱與滿刻度說明. 圖例由呼叫端放置, 例如
// GPU 卡片的標題列.
export const ChartLegend = memo(function ChartLegend({ series }: { series: ChartSeries[] }) {
  return (
    <div className="chart-legend">
      {series.map((s) => (
        <span key={s.key} className="chart-legend-item">
          <span className="chart-legend-swatch" style={{ background: `var(${s.color})` }} />
          {s.label}
          {s.scaleLabel && <span className="chart-legend-scale"> {s.scaleLabel}</span>}
        </span>
      ))}
    </div>
  );
});

// 曲線圖的高度由外層的版面決定, 繪圖區的寬與高皆隨外框改變而重繪.
function ChartImpl({ from, to, data, series, ariaLabel, gapTitle, onWidth, onHover, pinned, onPin, pinHint, edge, onZoom }: ChartProps) {
  const boxRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const { width, height } = size;
  const [theme, setTheme] = useState(0);
  const [hover, setHover] = useState<Hover | null>(null);
  const pointer = useRef<number | null>(null);
  const edgeRef = useRef(edge);
  edgeRef.current = edge;
  const zoomRef = useRef(onZoom);
  zoomRef.current = onZoom;
  const widthRef = useRef(0);
  widthRef.current = width;
  const zone = useRef(0);
  const reported = useRef(false);
  const dwell = useRef<number | undefined>(undefined);
  const fromRef = useRef(from);
  fromRef.current = from;
  const toRef = useRef(to);
  toRef.current = to;
  const gesture = useRef<Gesture | null>(null);
  const skipClick = useRef(false);
  const [held, setHeld] = useState(false);
  const [panning, setPanning] = useState(false);
  const flinging = edge?.flinging ?? false;
  const busy = panning || flinging;

  // 清除邊緣區的停留計時, 並在已回報捲動時回報停止.
  const resetZone = useCallback(() => {
    window.clearTimeout(dwell.current);
    dwell.current = undefined;
    zone.current = 0;
    if (reported.current) {
      reported.current = false;
      edgeRef.current?.onEdge(0);
    }
  }, []);

  // 結束按住的手勢. fling 為真時依放開前的速度啟動慣性捲動, 取消, 失去焦點與失去
  // pointer capture 時不滑動.
  const finishGesture = useCallback((el: HTMLElement | null, pointerId: number | null, fling: boolean) => {
    const g = gesture.current;
    gesture.current = null;
    if (!g || !g.pannable) {
      return;
    }
    if (el && pointerId !== null && el.hasPointerCapture(pointerId)) {
      el.releasePointerCapture(pointerId);
    }
    setHeld(false);
    edgeRef.current?.onHold(false);
    if (!g.panning) {
      return;
    }
    setPanning(false);
    if (!fling) {
      return;
    }
    const now = performance.now();
    const win = g.samples.filter((s) => now - s.t <= VELOCITY_WINDOW_MS);
    if (win.length < 2) {
      return;
    }
    const dt = (now - win[0].t) / 1000;
    const vx = (win[win.length - 1].x - win[0].x) / Math.max(dt, 0.001);
    if (Math.abs(vx) >= FLING_MIN_SPEED) {
      edgeRef.current?.onFling(vx, widthRef.current);
    }
  }, []);

  // 卸載與視窗失去焦點時停止邊緣捲動, 並結束按住的手勢.
  useEffect(() => {
    const onBlur = () => {
      resetZone();
      finishGesture(boxRef.current, null, false);
    };
    window.addEventListener("blur", onBlur);
    return () => {
      window.removeEventListener("blur", onBlur);
      resetZone();
    };
  }, [resetZone, finishGesture]);

  // 滾輪與捏合. React 的 wheel 監聽為 passive, 無法 preventDefault, 故直接掛在元素上.
  // 按住 Ctrl (macOS 另可按住 ⌘) 的滾輪縮放, 不捲動; Chromium 核心的 WebView 亦以此形式
  // 送出觸控板捏合. 其餘只攔截橫向移動與 Shift 加滾輪 (歷史畫面), 純垂直滾動留給內容區.
  // macOS 的 WKWebView 可能改以 gesturestart, gesturechange, gestureend 送出捏合, 一併
  // 處理; 捏合進行中略過按住 Ctrl 的滾輪, 避免重複縮放.
  const hasEdge = edge !== undefined;
  const hasZoom = onZoom !== undefined;
  useEffect(() => {
    const box = boxRef.current;
    if (!box || (!hasEdge && !hasZoom)) {
      return;
    }
    let pinch: number | null = null;
    const at = (clientX: number | undefined) => {
      const rect = box.getBoundingClientRect();
      const x = clientX === undefined ? (pointer.current ?? rect.width / 2) : clientX - rect.left;
      return Math.min(1, Math.max(0, x / Math.max(1, rect.width)));
    };
    const onWheel = (e: WheelEvent) => {
      if (e.ctrlKey || (isMac && e.metaKey)) {
        const zoom = zoomRef.current;
        if (!zoom) {
          return;
        }
        e.preventDefault();
        let d = e.deltaY;
        if (pinch !== null || d === 0) {
          return;
        }
        if (e.deltaMode === 1) {
          d *= WHEEL_LINE;
        } else if (e.deltaMode === 2) {
          d *= box.clientHeight;
        }
        zoom(Math.pow(ZOOM_WHEEL_FACTOR, d / ZOOM_WHEEL_UNIT), at(e.clientX));
        return;
      }
      const ed = edgeRef.current;
      if (!ed || (!ed.canLeft && !ed.canRight)) {
        return;
      }
      let d = e.deltaX;
      if (d === 0 && e.shiftKey) {
        d = e.deltaY;
      }
      if (d === 0) {
        return;
      }
      if (e.deltaMode === 1) {
        d *= WHEEL_LINE;
      } else if (e.deltaMode === 2) {
        d *= widthRef.current;
      }
      e.preventDefault();
      ed.onPan(d / Math.max(1, widthRef.current));
    };
    const onGestureStart = (e: Event) => {
      if (!zoomRef.current) {
        return;
      }
      e.preventDefault();
      pinch = (e as WebKitGesture).scale || 1;
    };
    const onGestureChange = (e: Event) => {
      const zoom = zoomRef.current;
      if (!zoom || pinch === null) {
        return;
      }
      e.preventDefault();
      const g = e as WebKitGesture;
      if (g.scale > 0) {
        zoom(pinch / g.scale, at(g.clientX));
        pinch = g.scale;
      }
    };
    const onGestureEnd = (e: Event) => {
      if (pinch !== null) {
        e.preventDefault();
        pinch = null;
      }
    };
    box.addEventListener("wheel", onWheel, { passive: false });
    box.addEventListener("gesturestart", onGestureStart);
    box.addEventListener("gesturechange", onGestureChange);
    box.addEventListener("gestureend", onGestureEnd);
    return () => {
      box.removeEventListener("wheel", onWheel);
      box.removeEventListener("gesturestart", onGestureStart);
      box.removeEventListener("gesturechange", onGestureChange);
      box.removeEventListener("gestureend", onGestureEnd);
    };
  }, [hasEdge, hasZoom]);

  useLayoutEffect(() => {
    const box = boxRef.current;
    if (!box) {
      return;
    }
    const measure = () => {
      const w = Math.floor(box.clientWidth);
      const h = Math.floor(box.clientHeight);
      setSize((old) => (old.width === w && old.height === h ? old : { width: w, height: h }));
    };
    const ro = new ResizeObserver(measure);
    ro.observe(box);
    measure();
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    if (width > 0) {
      onWidth?.(width);
    }
  }, [width, onWidth]);

  useEffect(() => onThemeChange(() => setTheme((n) => n + 1)), []);

  useEffect(() => {
    const c = canvasRef.current;
    if (c && width > 0 && height > 0) {
      draw(c, width, height, from, to, data, series);
    }
  }, [width, height, from, to, data, series, theme]);

  const describe = useCallback(
    (x: number): Hover | null => {
      if (width <= 0) {
        return null;
      }
      const span = Math.max(1, to - from);
      const t = from + (x / width) * span;
      if (data) {
        const gap = data.gaps.find((g) => t >= g.start && t <= g.end);
        if (gap) {
          return {
            x,
            line: false,
            lines: (
              <>
                <div className="chart-tip-head">{gap.title ?? gapTitle(formatClock(gap.start), formatClock(gap.end))}</div>
                <div>{gap.detail}</div>
                {pinHint && <div className="chart-tip-hint">{pinHint}</div>}
              </>
            ),
          };
        }
        const i = nearestIndex(data, series, t, (span / width) * 8);
        if (i >= 0) {
          const px = ((pointTime(data, i) - from) / span) * width;
          return {
            x: px,
            line: true,
            lines: (
              <>
                <div className="chart-tip-head">{formatClock(pointTime(data, i))}</div>
                {series.map((s) => (
                  <div key={s.key} className="chart-tip-row">
                    <span className="chart-tip-swatch" style={{ background: `var(${s.color})` }} />
                    <span className="chart-tip-name">{s.label}</span>
                    <span className="chart-tip-value">{s.format(i)}</span>
                  </div>
                ))}
                {pinHint && <div className="chart-tip-hint">{pinHint}</div>}
              </>
            ),
          };
        }
      }
      return null;
    },
    [data, series, from, to, width, gapTitle, pinHint],
  );

  // 資料更新時, 滑鼠仍停在圖上則以新資料重算懸停讀數.
  useEffect(() => {
    if (pointer.current !== null) {
      setHover(describe(pointer.current));
    }
  }, [describe]);

  // 捲動使顯示範圍移動時, 游標不動而所指的時刻改變, 須重新回報.
  useEffect(() => {
    if (pointer.current !== null && width > 0) {
      onHover?.(from + (pointer.current / width) * (to - from));
    }
    // 只在顯示範圍改變時回報.
  }, [from, to]);

  // 依游標位置判斷是否進入或離開邊緣區. 按住與慣性捲動期間不判斷.
  const checkZone = useCallback(
    (x: number) => {
      const z = x <= EDGE_ZONE ? -1 : x >= widthRef.current - EDGE_ZONE ? 1 : 0;
      if (z !== zone.current) {
        resetZone();
        zone.current = z;
        if (z !== 0) {
          dwell.current = window.setTimeout(() => {
            dwell.current = undefined;
            reported.current = true;
            edgeRef.current?.onEdge(z);
          }, EDGE_DWELL_MS);
        }
      }
    },
    [resetZone],
  );

  // 按住與慣性捲動結束後, 游標仍在邊緣區則重新計時.
  useEffect(() => {
    if (hasEdge && !held && !flinging && pointer.current !== null) {
      checkZone(pointer.current);
    }
  }, [hasEdge, held, flinging, checkZone]);

  const onMove = (e: React.MouseEvent<HTMLDivElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const x = Math.min(Math.max(0, e.clientX - rect.left), rect.width);
    pointer.current = x;
    if (edge && !gesture.current?.pannable && !edge.flinging) {
      checkZone(x);
    }
    setHover(describe(x));
    onHover?.(from + (x / Math.max(1, width)) * (to - from));
  };
  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!edge || e.button !== 0) {
      return;
    }
    const stopped = edge.onStop();
    skipClick.current = stopped;
    const pannable = edge.scrollable;
    gesture.current = {
      x0: e.clientX,
      y0: e.clientY,
      from0: from,
      span0: Math.max(1, to - from),
      pannable,
      panning: false,
      samples: [{ t: performance.now(), x: e.clientX }],
    };
    if (pannable) {
      e.currentTarget.setPointerCapture(e.pointerId);
      resetZone();
      setHeld(true);
      edge.onHold(true);
    }
  };
  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const g = gesture.current;
    if (!g) {
      return;
    }
    const now = performance.now();
    g.samples.push({ t: now, x: e.clientX });
    while (g.samples.length > 2 && now - g.samples[0].t > 2 * VELOCITY_WINDOW_MS) {
      g.samples.shift();
    }
    if (!g.panning && Math.hypot(e.clientX - g.x0, e.clientY - g.y0) > DRAG_THRESHOLD) {
      skipClick.current = true;
      if (g.pannable) {
        g.panning = true;
        setPanning(true);
      }
    }
    if (g.panning) {
      edgeRef.current?.onDrag(g.from0 - ((e.clientX - g.x0) / Math.max(1, widthRef.current)) * g.span0);
    }
  };
  const onPointerUp = (e: React.PointerEvent<HTMLDivElement>) => finishGesture(e.currentTarget, e.pointerId, true);
  const onPointerCancel = (e: React.PointerEvent<HTMLDivElement>) => finishGesture(e.currentTarget, e.pointerId, false);
  const onClick = (e: React.MouseEvent<HTMLDivElement>) => {
    if (skipClick.current) {
      skipClick.current = false;
      return;
    }
    if (!onPin) {
      return;
    }
    const rect = e.currentTarget.getBoundingClientRect();
    const x = Math.min(Math.max(0, e.clientX - rect.left), rect.width);
    onPin(from + (x / Math.max(1, width)) * (to - from));
  };
  const onLeave = () => {
    resetZone();
    pointer.current = null;
    setHover(null);
    onHover?.(null);
  };

  const pinX =
    pinned !== null && pinned !== undefined && width > 0 && pinned >= from && pinned <= to
      ? ((pinned - from) / Math.max(1, to - from)) * width
      : null;
  const tipLeft = hover && width > 0 && hover.x > width * 0.62 ? undefined : hover ? hover.x + 8 : undefined;
  const tipRight = hover && width > 0 && hover.x > width * 0.62 ? width - hover.x + 8 : undefined;

  return (
    <div className="chart">
      <div className="chart-yaxis">
        <span>100</span>
        <span>50</span>
        <span>0</span>
      </div>
      <div
        className={panning ? "chart-plot panning" : edge?.scrollable ? "chart-plot pannable" : "chart-plot"}
        ref={boxRef}
        onMouseMove={onMove}
        onMouseLeave={onLeave}
        onClick={onClick}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerCancel}
        onLostPointerCapture={onPointerCancel}
      >
        <canvas ref={canvasRef} role="img" aria-label={ariaLabel} />
        {pinX !== null && <div className="chart-pin" style={{ left: pinX }} />}
        {edge?.canLeft && (
          <div className="chart-edge chart-edge-left" aria-hidden="true">
            <span className={edge.active < 0 ? "chart-edge-arrow active" : "chart-edge-arrow"}>‹</span>
          </div>
        )}
        {edge?.canRight && (
          <div className="chart-edge chart-edge-right" aria-hidden="true">
            <span className={edge.active > 0 ? "chart-edge-arrow active" : "chart-edge-arrow"}>›</span>
          </div>
        )}
        {!busy && hover?.line && <div className="chart-cursor" style={{ left: hover.x }} />}
        {!busy && hover && (
          <div className="chart-tip" style={{ left: tipLeft, right: tipRight }}>
            {hover.lines}
          </div>
        )}
      </div>
      <div className="chart-xaxis">
        {ticks(from, to).map((s, i) => (
          <span key={i}>{s}</span>
        ))}
      </div>
    </div>
  );
}

export const Chart = memo(ChartImpl);
