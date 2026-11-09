import { useEffect, useId, useMemo, useState } from "react";
import { backend } from "../api/backend";
import { serviceError } from "../api/errors";
import type { RecordingInfo, TimeRange } from "../api/types";
import { ArrowLeftIcon } from "../components/Icons";
import { formatClock, formatDateTime, parseDateTime, rangeMicros } from "../format";
import { useT } from "../i18n";
import { RangeSelect } from "../monitor/controls";
import {
  choicesStore,
  defaultCurves,
  historyStore,
  setHistoryLeft,
  setHistoryRange,
  settingsStore,
  showPage,
} from "../state/app";
import { useStore } from "../state/store";
import { HistoryBlock } from "./HistoryBlock";

const historyRanges: TimeRange[] = ["1m", "5m", "15m", "30m", "60m", "3h", "6h", "12h", "24h", "all"];
const minSpan = 1e6;

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

// HistoryView 為歷史畫面: 頂端為工具列, 其下為每個記錄檔一個的分組, 底端為時間捲動條.
// 所有分組共用同一個顯示範圍, 捲動範圍為所開啟檔案中最早的開始時刻到最晚的結束時刻.
export function HistoryView() {
  const t = useT();
  const paths = useStore(historyStore, (h) => h.paths);
  const storedLeft = useStore(historyStore, (h) => h.left);
  const range = useStore(settingsStore, (s) => s?.history_range) ?? "5m";
  const curves = useStore(settingsStore, (s) => s?.curves) ?? defaultCurves;
  const collapsed = useStore(settingsStore, (s) => s?.collapsed_gpus);
  const collapsedProcs = useStore(settingsStore, (s) => s?.collapsed_procs);
  const ranges = useStore(choicesStore, (c) => c?.history_ranges) ?? historyRanges;
  const loaded = useInfos(paths);

  const infos = useMemo(() => (loaded ?? []).flatMap((l) => ("info" in l ? [l.info] : [])), [loaded]);
  const min = infos.length > 0 ? Math.min(...infos.map((i) => i.start)) : 0;
  const max = infos.length > 0 ? Math.max(...infos.map((i) => i.end)) : 0;
  const total = Math.max(minSpan, max - min);
  const span = range === "all" ? total : rangeMicros(range) || rangeMicros("5m");
  const maxLeft = Math.max(min, max - span);
  const left = range === "all" ? min : Math.min(maxLeft, Math.max(min, storedLeft ?? maxLeft));
  const to = left + span;

  const onRange = (r: TimeRange) => {
    // 變更顯示範圍時維持目前畫面的中心.
    const next = r === "all" ? total : rangeMicros(r);
    setHistoryLeft(left + span / 2 - next / 2);
    void setHistoryRange(r);
  };

  const sameDay = formatDateTime(min).slice(0, 10) === formatDateTime(max).slice(0, 10);
  const edgeText = (us: number) => (sameDay ? formatClock(us) : formatDateTime(us));
  const scrollable = infos.length > 0 && maxLeft > min && range !== "all";

  return (
    <main className="main">
      <div className="toolbar">
        <button type="button" className="btn btn-ghost" onClick={() => showPage("recordings")}>
          <ArrowLeftIcon />
          {t("history.back")}
        </button>
        <span className="toolbar-title">{t("history.title")}</span>
        <span className="flex1" />
        {infos.length > 0 && (
          <JumpForm
            key={paths.join("\n")}
            initial={formatDateTime(Math.round((left + span / 2) / 1e6) * 1e6)}
            onJump={(us) => setHistoryLeft(us - span / 2)}
          />
        )}
        <RangeSelect value={range} ranges={ranges} onChange={onRange} />
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
        {infos.length > 0 && (
          <div className="host-block scrubber">
            <span className="scrub-edge">{edgeText(min)}</span>
            <label htmlFor="history-scrub" className="sr-only">
              {t("history.position")}
            </label>
            <input
              id="history-scrub"
              type="range"
              className="scrub-input"
              min={0}
              max={Math.max(1, Math.round((maxLeft - min) / 1000))}
              step={1}
              value={Math.round((left - min) / 1000)}
              disabled={!scrollable}
              onChange={(e) => setHistoryLeft(min + Number(e.target.value) * 1000)}
            />
            <span className="scrub-edge">{edgeText(max)}</span>
          </div>
        )}
      </div>
    </main>
  );
}
