import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { backend } from "../api/backend";
import { serviceError } from "../api/errors";
import type { RecordingInfo } from "../api/types";
import type { ChartEdge } from "../chart/Chart";
import { ArrowLeftIcon } from "../components/Icons";
import { formatClock, formatDateTime, parseDateTime } from "../format";
import { useT } from "../i18n";
import { ZoomControls } from "../monitor/controls";
import {
  choicesStore,
  defaultCurves,
  historyStore,
  setHistoryLeft,
  setHistoryPinned,
  setHistorySpan,
  settingsStore,
  showPage,
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

type Loaded = { path: string; info: RecordingInfo } | { path: string; error: string };

// useInfos 讀取開啟中記錄檔的說明, 結果依 paths 的順序排列. 連線中的記錄檔只讀取開啟
// 當下已存檔的內容, 之後不再更新.
function useInfos(paths: string[]): Loaded[] | null {
  const [loaded, setLoaded] = useState<Loaded[] | null>(null);
  const key = paths.join("\n");
  useEffect(() => {
    let alive = true;
    setLoaded(null);
    Promise.all(
      paths.map(async (path): Promise<Loaded> => {
        try {
          return { path, info: await backend.recordingInfo(path) };
        } catch (err) {
          return { path, error: serviceError(err).message };
        }
      }),
    ).then((l) => {
      if (alive) {
        setLoaded(l);
      }
    });
    return () => {
      alive = false;
    };
    // paths 以 key 代表其內容.
  }, [key]);
  return loaded;
}

function baseName(path: string): string {
  return path.split(/[\\/]/).pop() ?? path;
}

function JumpForm({ onJump, initial }: { onJump: (us: number) => void; initial: string }) {
  const t = useT();
  const id = useId();
  const [text, setText] = useState(initial);
  const [invalid, setInvalid] = useState(false);
  const submit = () => {
    const us = parseDateTime(text);
    setInvalid(us === null);
    if (us !== null) {
      onJump(us);
    }
  };
  return (
    <form
      className="ctl"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <label htmlFor={id} className="ctl-label">
        {t("history.goTo")}
      </label>
      <input
        id={id}
        type="text"
        className="input jump-input"
        value={text}
        aria-invalid={invalid}
        title={invalid ? t("history.goInvalid") : undefined}
        spellCheck={false}
        onChange={(e) => {
          setText(e.target.value);
          setInvalid(false);
        }}
      />
      <button type="submit" className="btn">
        {t("history.go")}
      </button>
      {invalid && (
        <span className="jump-error" role="alert">
          {t("history.goInvalid")}
        </span>
      )}
    </form>
  );
}

// HistoryView 為歷史畫面: 頂端為工具列, 其下為每個記錄檔一個的分組. 時間捲動由曲線圖
// 邊緣自動捲動與橫向滾動達成.
// 所有分組共用同一個顯示範圍, 捲動範圍為所開啟檔案中最早的開始時刻到最晚的結束時刻.
export function HistoryView() {
  const t = useT();
  const paths = useStore(historyStore, (h) => h.paths);
  const storedLeft = useStore(historyStore, (h) => h.left);
  const spanMS = useStore(settingsStore, (s) => s?.history_span_ms) ?? defaultSpanMS;
  const allSetting = useStore(settingsStore, (s) => s?.history_all) ?? false;
  const minMS = useStore(choicesStore, (c) => c?.min_span_ms) ?? defaultMinMS;
  const curves = useStore(settingsStore, (s) => s?.curves) ?? defaultCurves;
  const collapsed = useStore(settingsStore, (s) => s?.collapsed_gpus);
  const collapsedProcs = useStore(settingsStore, (s) => s?.collapsed_procs);
  const pinned = useStore(historyStore, (h) => h.pinned);
  const loaded = useInfos(paths);

  // 離開歷史畫面時清除固定時刻.
  useEffect(() => () => setHistoryPinned(null), []);

  // 有固定時刻且畫面上沒有對話框時, Esc 清除固定時刻. 以擷取階段監聽, 使對話框自己的
  // Esc 處理先關閉對話框之前即可判斷.
  useEffect(() => {
    if (pinned === null) {
      return;
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !document.querySelector('[role="dialog"]')) {
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

  const infos = useMemo(() => (loaded ?? []).flatMap((l) => ("info" in l ? [l.info] : [])), [loaded]);
  const min = infos.length > 0 ? Math.min(...infos.map((i) => i.start)) : 0;
  const max = infos.length > 0 ? Math.max(...infos.map((i) => i.end)) : 0;
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

  const scrollable = infos.length > 0 && maxLeft > min && !all;

  // 捲動與縮放所需的最新值. 滾輪與自動捲動可能在重新繪製之間連續觸發, 位置與長度以此為準.
  const view = useRef({ left, span, min, max, total, minLen, maxLeft, scrollable });
  view.current = { left, span, min, max, total, minLen, maxLeft, scrollable };
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
    }),
    [scrollable, flinging, left, min, maxLeft, dir, onPan, stopFling, onDrag, onFling],
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
  const onButtonZoom = useCallback((factor: number) => zoom(factor, 0.5), [zoom]);
  const showAll = () => {
    stopFling();
    setHistoryLeft(min);
    setHistorySpan(total / 1000, true, minMS);
  };

  const sameDay = formatDateTime(min).slice(0, 10) === formatDateTime(max).slice(0, 10);
  const edgeText = (us: number) => (sameDay ? formatClock(us) : formatDateTime(us));

  return (
    <main className="main">
      <div className="toolbar">
        <button type="button" className="btn btn-ghost" onClick={() => showPage("recordings")}>
          <ArrowLeftIcon />
          {t("history.back")}
        </button>
        <span className="toolbar-title">{t("history.title")}</span>
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
        <span className="flex1" />
        {infos.length > 0 && (
          <>
            <JumpForm
              key={paths.join("\n")}
              initial={formatDateTime(Math.round((left + span / 2) / 1e6) * 1e6)}
              onJump={(us) => {
                stopFling();
                setHistoryLeft(us - span / 2);
                setHistoryPinned(Math.min(max, Math.max(min, us)));
              }}
            />
            <ZoomControls span={span} min={minLen} max={total} outDisabled={all} onZoom={onButtonZoom} />
            <button type="button" className="btn" aria-pressed={all} onClick={showAll}>
              {t("history.showAll")}
            </button>
          </>
        )}
      </div>
      <div className="content history-content">
        {loaded === null && <div className="empty-note">{t("history.loading")}</div>}
        {loaded?.map((l) =>
          "info" in l ? (
            <HistoryBlock
              key={l.path}
              info={l.info}
              from={left}
              to={to}
              curves={curves}
              collapsedGPUs={collapsed?.[l.info.host]}
              procsCollapsed={collapsedProcs?.includes(l.info.host) ?? false}
              pinned={pinned}
              onPin={setHistoryPinned}
              edge={edge}
              onZoom={zoom}
            />
          ) : (
            <div key={l.path} className="history-group">
              <header className="history-group-head">
                <span className="history-group-title">{baseName(l.path)}</span>
              </header>
              <div className="card card-note">
                {t("history.readFailed")} {l.error}
              </div>
            </div>
          ),
        )}
      </div>
    </main>
  );
}
