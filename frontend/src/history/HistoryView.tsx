import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { backend } from "../api/backend";
import { serviceError } from "../api/errors";
import type { RecordingInfo } from "../api/types";
import type { ChartEdge } from "../chart/Chart";
import { ArrowLeftIcon } from "../components/Icons";
import { formatClock, formatDateRange, formatDateTime, formatLength } from "../format";
import { useT } from "../i18n";
import {
  choicesStore,
  defaultCurves,
  historyStore,
  selectHistoryMax,
  setHistoryLeft,
  setHistoryPinned,
  setHistorySpan,
  settingsStore,
  showPage,
  type MaxSelection,
} from "../state/app";
import { useStore } from "../state/store";
import { HistoryBlock } from "./HistoryBlock";

// 捲動範圍長度的下限 (微秒), 避免所開啟的記錄檔只有一筆記錄時長度為零.
const minSpan = 1e6;
const defaultSpanMS = 300_000;
const defaultMinMS = 60_000;
// 邊緣自動捲動每秒移動顯示範圍長度的比例.
const edgeSpeed = 0.1;
// 慣性捲動速度的指數衰減時間常數 (秒), 與速度低於此值 (像素/秒) 即停止.
const flingTau = 0.2;
const flingStopSpeed = 20;

type Loaded = { info: RecordingInfo } | { error: string };

// useInfo 讀取開啟中記錄檔的說明. 連線中的記錄檔只讀取開啟當下已存檔的內容, 之後不再
// 更新.
function useInfo(path: string | null): Loaded | null {
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  useEffect(() => {
    if (path === null) {
      return;
    }
    let alive = true;
    setLoaded(null);
    backend.recordingInfo(path).then(
      (info) => alive && setLoaded({ info }),
      (err) => alive && setLoaded({ error: serviceError(err).message }),
    );
    return () => {
      alive = false;
    };
  }, [path]);
  return loaded;
}

function baseName(path: string): string {
  return path.split(/[\\/]/).pop() ?? path;
}

// HistoryView 為歷史畫面: 頂端為工具列, 其下為開啟中記錄檔的 GPU 卡片與程序卡片.
// 時間捲動由曲線圖邊緣自動捲動與橫向滾動達成. 捲動範圍為該記錄檔的開始時刻到結束時刻.
export function HistoryView() {
  const t = useT();
  const path = useStore(historyStore, (h) => h.path);
  const storedLeft = useStore(historyStore, (h) => h.left);
  const spanMS = useStore(settingsStore, (s) => s?.history_span_ms) ?? defaultSpanMS;
  const allSetting = useStore(settingsStore, (s) => s?.history_all) ?? false;
  const minMS = useStore(choicesStore, (c) => c?.min_span_ms) ?? defaultMinMS;
  const curves = useStore(settingsStore, (s) => s?.curves) ?? defaultCurves;
  const collapsed = useStore(settingsStore, (s) => s?.collapsed_gpus);
  const collapsedProcs = useStore(settingsStore, (s) => s?.collapsed_procs);
  const collapsedMaxima = useStore(settingsStore, (s) => s?.collapsed_maxima);
  const pinned = useStore(historyStore, (h) => h.pinned);
  const maxSel = useStore(historyStore, (h) => h.maxSel);
  const loaded = useInfo(path);

  // 有固定時刻且畫面上沒有對話框時, Esc 清除固定時刻. 以擷取階段監聽, 使對話框自己的
  // Esc 處理先關閉對話框之前即可判斷. 曲線圖選取時段時的 Esc 只取消選取: 選取中, 或
  // 曲線圖已先處理並 preventDefault 時不清除.
  const selectingRef = useRef(false);
  useEffect(() => {
    if (pinned === null) {
      return;
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !e.defaultPrevented && !selectingRef.current && !document.querySelector('[role="dialog"]')) {
        setHistoryPinned(null);
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [pinned !== null]);

  const flingId = useRef<number | null>(null);
  const [flinging, setFlinging] = useState(false);
  // 停止慣性捲動, 回傳先前是否正在滑動.
  const stopFling = useCallback((): boolean => {
    if (flingId.current === null) {
      return false;
    }
    cancelAnimationFrame(flingId.current);
    flingId.current = null;
    setFlinging(false);
    return true;
  }, []);
  useEffect(() => {
    window.addEventListener("blur", stopFling);
    return () => {
      window.removeEventListener("blur", stopFling);
      stopFling();
    };
  }, [stopFling]);

  const info = loaded && "info" in loaded ? loaded.info : null;
  const min = info?.start ?? 0;
  const max = info?.end ?? 0;
  // 顯示範圍的長度最短 1 分鐘, 最長為捲動範圍的長度; 捲動範圍短於 1 分鐘時即為捲動範圍
  // 的長度. 處於顯示全部狀態, 或記住的長度不短於捲動範圍時, 顯示範圍涵蓋整個捲動範圍.
  const total = Math.max(minSpan, max - min);
  const minLen = Math.min(minMS * 1000, total);
  const stored = Math.max(minLen, spanMS * 1000);
  const all = allSetting || stored >= total;
  const span = all ? total : stored;
  const maxLeft = Math.max(min, max - span);
  const left = all ? min : Math.min(maxLeft, Math.max(min, storedLeft ?? maxLeft));
  const to = left + span;

  const scrollable = info !== null && maxLeft > min && !all;

  // 捲動與縮放所需的最新值. 滾輪與自動捲動可能在重新繪製之間連續觸發, 位置與長度以此為準.
  const view = useRef({ left, span, min, max, total, minLen, maxLeft, scrollable, all });
  view.current = { left, span, min, max, total, minLen, maxLeft, scrollable, all };
  const moveBy = useCallback((delta: number): boolean => {
    const v = view.current;
    const next = Math.min(v.maxLeft, Math.max(v.min, v.left + delta));
    if (next === v.left) {
      return false;
    }
    v.left = next;
    setHistoryLeft(next);
    return true;
  }, []);

  const [edgeDir, setEdgeDir] = useState(0);
  const [held, setHeld] = useState(false);
  const dir = scrollable && !held && !flinging ? edgeDir : 0;
  useEffect(() => {
    if (dir === 0) {
      return;
    }
    let last = performance.now();
    let id = requestAnimationFrame(function step(now) {
      const dt = now - last;
      last = now;
      if (!moveBy(dir * edgeSpeed * view.current.span * (dt / 1000))) {
        setEdgeDir(0);
        return;
      }
      id = requestAnimationFrame(step);
    });
    return () => cancelAnimationFrame(id);
  }, [dir, moveBy]);

  const onPan = useCallback(
    (frac: number) => {
      stopFling();
      void moveBy(frac * view.current.span);
    },
    [moveBy, stopFling],
  );
  const onDrag = useCallback(
    (next: number) => {
      stopFling();
      void moveBy(next - view.current.left);
    },
    [moveBy, stopFling],
  );
  const onFling = useCallback(
    (vx: number, width: number) => {
      stopFling();
      const w = Math.max(1, width);
      // v 為顯示範圍長度的倍數/秒, 曲線跟著手移動, 故與手的方向相反.
      let v = -vx / w;
      const minV = flingStopSpeed / w;
      let last = performance.now();
      const step = (now: number) => {
        const dt = Math.min(0.25, (now - last) / 1000);
        last = now;
        const decay = Math.exp(-dt / flingTau);
        const moved = moveBy(v * view.current.span * flingTau * (1 - decay));
        v *= decay;
        if (!moved || Math.abs(v) < minV) {
          flingId.current = null;
          setFlinging(false);
          return;
        }
        flingId.current = requestAnimationFrame(step);
      };
      flingId.current = requestAnimationFrame(step);
      setFlinging(true);
    },
    [moveBy, stopFling],
  );
  // fitRange 把選取的時段換算為放開後的顯示範圍: 長度取整至毫秒, 短於下限時以時段中心
  // 擴展至下限, 超出捲動範圍時平移至捲動範圍內; 長度達捲動範圍的長度即為整個捲動範圍.
  const fitRange = useCallback((a: number, b: number): [number, number] => {
    const v = view.current;
    const lo = Math.max(v.min, Math.min(a, b));
    const hi = Math.min(v.max, Math.max(a, b));
    const len = Math.min(v.total, Math.max(v.minLen, Math.round((hi - lo) / 1000) * 1000));
    if (len >= v.total) {
      return [v.min, v.min + v.total];
    }
    const left = Math.max(v.min, Math.min(v.max - len, (lo + hi) / 2 - len / 2));
    return [left, left + len];
  }, []);
  // onRange 以選取的時段 (fitRange 的結果) 為新的顯示範圍, 進出顯示全部狀態的規則與 zoom 相同.
  const onRange = useCallback(
    (from: number, to: number) => {
      stopFling();
      const v = view.current;
      const next = to - from;
      const toAll = next >= v.total;
      const nextLeft = toAll ? v.min : from;
      v.span = next;
      v.left = nextLeft;
      v.maxLeft = Math.max(v.min, v.max - next);
      setHistoryLeft(nextLeft);
      setHistorySpan(next / 1000, toAll, minMS);
    },
    [stopFling, minMS],
  );
  const onSelecting = useCallback((on: boolean) => {
    selectingRef.current = on;
  }, []);
  const rangeLabel = useCallback(
    (from: number, to: number) =>
      t("history.selectRange", { from: formatClock(from), to: formatClock(to), length: formatLength(to - from, t) }),
    [t],
  );
  const edge = useMemo(
    (): ChartEdge => ({
      canLeft: scrollable && left > min,
      canRight: scrollable && left < maxLeft,
      active: dir,
      scrollable,
      flinging,
      onEdge: setEdgeDir,
      onPan,
      onHold: setHeld,
      onStop: stopFling,
      onDrag,
      onFling,
      fitRange,
      onSelecting,
      onRange,
      rangeLabel,
    }),
    [scrollable, flinging, left, min, maxLeft, dir, onPan, stopFling, onDrag, onFling, fitRange, onSelecting, onRange, rangeLabel],
  );

  // zoom 以 factor 倍變更顯示範圍的長度, 限制於上下限之間, 並使位於 at (佔顯示範圍寬度的
  // 比例) 的時刻維持不動; 結果超出捲動範圍時平移至捲動範圍內. 長度達捲動範圍的長度即進入
  // 顯示全部狀態, 縮短即離開.
  const zoom = useCallback(
    (factor: number, at: number) => {
      stopFling();
      const v = view.current;
      let next = Math.min(v.total, Math.max(v.minLen, v.span * factor));
      if (next < v.total) {
        next = Math.min(v.total, Math.max(v.minLen, Math.round(next / 1000) * 1000));
      }
      const toAll = next >= v.total;
      const anchor = v.left + at * v.span;
      const nextLeft = toAll ? v.min : Math.min(v.max - next, Math.max(v.min, anchor - at * next));
      v.span = next;
      v.left = nextLeft;
      v.maxLeft = Math.max(v.min, v.max - next);
      setHistoryLeft(nextLeft);
      setHistorySpan(next / 1000, toAll, minMS);
    },
    [stopFling, minMS],
  );
  const showAll = () => {
    stopFling();
    setHistoryLeft(min);
    setHistorySpan(total / 1000, true, minMS);
  };

  const sameDay = info !== null && formatDateTime(min).slice(0, 10) === formatDateTime(max).slice(0, 10);
  const edgeText = useCallback((us: number) => (sameDay ? formatClock(us) : formatDateTime(us)), [sameDay]);

  // pickMax 為最大值卡片的點選: 選取該格並固定至所選出現處的時刻 t. 顯示範圍的長度不變,
  // 平移至 t 位於正中央, 超出捲動範圍時停在邊界; 處於顯示全部狀態時不平移. 平移前停止
  // 慣性捲動與邊緣自動捲動.
  const pickMax = useCallback(
    (sel: MaxSelection, t: number) => {
      stopFling();
      setEdgeDir(0);
      const v = view.current;
      if (!v.all) {
        const next = Math.min(v.maxLeft, Math.max(v.min, t - v.span / 2));
        if (next !== v.left) {
          v.left = next;
          setHistoryLeft(next);
        }
      }
      selectHistoryMax(sel, t);
    },
    [stopFling],
  );

  // 標題為記錄檔的主機名稱, user@addr 與起訖時刻; 讀取完成之前為 "History", 讀取失敗時為檔名.
  let titleText = t("history.title");
  let titleNode = <span className="toolbar-title">{titleText}</span>;
  if (info) {
    const host = info.devices?.hostname || info.host;
    const range = formatDateRange(info.start, info.end);
    titleText = `${host} ${info.host} ${range}`;
    titleNode = (
      <>
        <span className="toolbar-title">{host}</span>
        <span className="history-title-detail">{info.host}</span>
        <span className="history-title-detail">{range}</span>
      </>
    );
  } else if (loaded && path !== null) {
    titleText = baseName(path);
    titleNode = <span className="toolbar-title">{titleText}</span>;
  }

  return (
    <main className="main">
      <div className="toolbar">
        <button type="button" className="btn btn-ghost" onClick={() => showPage("recordings")}>
          <ArrowLeftIcon />
          {t("history.back")}
        </button>
        <span className="history-head">
          <span className="history-title" title={titleText}>
            {titleNode}
          </span>
          {pinned !== null && (
            <span className="pin-tag">
              {t("history.pinned", { time: edgeText(pinned) })}
              <button
                type="button"
                className="pin-tag-clear"
                aria-label={t("history.clearPin")}
                title={t("history.clearPin")}
                onClick={() => setHistoryPinned(null)}
              >
                ✕
              </button>
            </span>
          )}
        </span>
        {info && (
          <button type="button" className="btn" aria-pressed={all} onClick={showAll}>
            {t("history.showAll")}
          </button>
        )}
      </div>
      <div className="content">
        {loaded === null && <div className="empty-note">{t("history.loading")}</div>}
        {loaded && "error" in loaded && (
          <div className="card card-note">
            {t("history.readFailed")} {loaded.error}
          </div>
        )}
        {info && (
          <HistoryBlock
            info={info}
            from={left}
            to={to}
            curves={curves}
            collapsedGPUs={collapsed?.[info.host]}
            procsCollapsed={collapsedProcs?.includes(info.host) ?? false}
            maximaCollapsed={collapsedMaxima?.includes(info.host) ?? false}
            pinned={pinned}
            onPin={setHistoryPinned}
            maxSel={maxSel}
            onMaxPick={pickMax}
            timeText={edgeText}
            edge={edge}
            onZoom={zoom}
          />
        )}
      </div>
    </main>
  );
}
