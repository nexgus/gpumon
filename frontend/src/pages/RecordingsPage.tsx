import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type MouseEvent, type Ref } from "react";
import { backend } from "../api/backend";
import { serviceError } from "../api/errors";
import type { RecordingFile, RecordingListing } from "../api/types";
import { ArrowLeftIcon, ChevronRight, DeleteIcon } from "../components/Icons";
import { formatDateRange, formatDateTime, formatDuration, formatSize } from "../format";
import { useT, type Translator } from "../i18n";
import { isMac } from "../platform";
import { openRecording, recordingsListStore, reportOperationFailure, useSizeUnits } from "../state/app";
import { useStore } from "../state/store";
import { DeleteRecordingsDialog } from "../dialogs/DeleteRecordingsDialog";

function fileCount(n: number, t: Translator): string {
  return n === 1 ? t("recordings.fileOne") : t("recordings.fileMany", { count: n });
}

function baseName(path: string): string {
  return path.split(/[\\/]/).pop() ?? path;
}

// rangeText 為檔案的起訖時刻; 寫入中的檔案終點顯示為 "現在".
function rangeText(f: RecordingFile, t: Translator): string {
  if (f.active) {
    return `${formatDateTime(f.start)} - ${t("recordings.now")}`;
  }
  return formatDateRange(f.start, f.end);
}

// SelectState 為一組列的選取狀況, 對應方框的空白, 橫線, 打勾三種外觀.
type SelectState = "none" | "some" | "all";

function selectState(paths: string[], selected: ReadonlySet<string>): SelectState {
  const n = paths.filter((p) => selected.has(p)).length;
  return n === 0 ? "none" : n === paths.length ? "all" : "some";
}

const noop = () => {};

// TriCheck 為三種外觀的方框; indeterminate 只能以 DOM 屬性設定. 變更由外層的點擊處理, 方框本身不處理.
function TriCheck({ state, label, disabled }: { state: SelectState; label: string; disabled?: boolean }) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (ref.current) {
      ref.current.indeterminate = state === "some";
    }
  });
  return (
    <input
      ref={ref}
      type="checkbox"
      className="check"
      aria-label={label}
      checked={state === "all"}
      disabled={disabled}
      onChange={noop}
    />
  );
}

// 按住 Shift 按下滑鼠時阻止預設的文字選取.
function noShiftSelect(e: MouseEvent) {
  if (e.shiftKey) {
    e.preventDefault();
  }
}

interface RowProps {
  selected: boolean;
  onRowClick: (e: MouseEvent) => void;
  onBoxClick: (e: MouseEvent) => void;
  muted?: boolean;
  label: string;
  note?: string;
  duration?: string;
  size: string;
  onOpen?: () => void;
  openText?: string;
  openRef?: Ref<HTMLButtonElement>;
}

// Row 為清單的一列. 點選方框範圍 (列的左緣至起訖時刻之前) 與 "開啟" 按鍵以外的位置選取該列,
// 點選方框範圍切換該列的選取; 修飾鍵的語意由呼叫端處理.
function Row({ selected, onRowClick, onBoxClick, muted, label, note, duration, size, onOpen, openText, openRef }: RowProps) {
  return (
    <li className={`rec-row${selected ? " checked" : ""}${muted ? " muted" : ""}`} onMouseDown={noShiftSelect} onClick={onRowClick}>
      <div
        className="rec-check"
        onClick={(e) => {
          e.stopPropagation();
          onBoxClick(e);
        }}
      >
        <TriCheck state={selected ? "all" : "none"} label={label} />
      </div>
      <span className="rec-label" title={label}>
        {label}
      </span>
      <span className="rec-note">{note}</span>
      <span className="rec-duration">{duration}</span>
      <span className="rec-size">{size}</span>
      {onOpen && (
        <button
          ref={openRef}
          type="button"
          className="btn btn-sm"
          onClick={(e) => {
            e.stopPropagation();
            onOpen();
          }}
        >
          {openText}
        </button>
      )}
    </li>
  );
}

// GroupCheck 為分組標題列左端的方框, 點擊該組全部已選則清除該組, 否則選取該組全部.
function GroupCheck({
  paths,
  selected,
  label,
  onChange,
}: {
  paths: string[];
  selected: ReadonlySet<string>;
  label: string;
  onChange: (fn: (sel: ReadonlySet<string>) => ReadonlySet<string>) => void;
}) {
  const state = selectState(paths, selected);
  return (
    <div
      className="rec-check"
      onClick={() =>
        onChange((sel) => {
          const next = new Set(sel);
          for (const p of paths) {
            if (state === "all") {
              next.delete(p);
            } else {
              next.add(p);
            }
          }
          return next;
        })
      }
    >
      <TriCheck state={state} label={label} />
    </div>
  );
}

// selectedCountText 為第一層一列的已選取數文字, 沒有選取時為空字串.
function selectedCountText(n: number, t: Translator): string {
  return n > 0 ? t("recordings.selectedCount", { count: n }) : "";
}

// HostRow 為第一層主機清單的一列, 由左至右為名稱, 已選取數 (有選取時), 檔案數, 總大小與向右
// 箭頭; 整列為一個按鍵, 點選或以 Enter, Space 進入該主機的第二層.
function HostRow({
  label,
  mono = false,
  selectedText,
  files,
  size,
  onOpen,
}: {
  label: string;
  mono?: boolean;
  selectedText: string;
  files: string;
  size: string;
  onOpen: () => void;
}) {
  return (
    <li>
      <button type="button" className="rec-hostrow" onClick={onOpen}>
        <span className={mono ? "rec-host ellipsis" : "rec-title ellipsis"} title={label}>
          {label}
        </span>
        {selectedText !== "" && <span className="sel-count">{selectedText}</span>}
        <span className="flex1" />
        <span className="rec-files">{files}</span>
        <span className="rec-size">{size}</span>
        <ChevronRight size={14} />
      </button>
    </li>
  );
}

// skippedKey 為 "無法開啟的檔案" 的識別; 主機的識別為 "h:" 加主機名稱.
const skippedKey = "s";

function hostKey(host: string): string {
  return `h:${host}`;
}

function groupKeys(l: RecordingListing): string[] {
  return [...l.hosts.map((g) => hostKey(g.host)), ...(l.skipped.length > 0 ? [skippedKey] : [])];
}

// RecordingsPage 為記錄檔清單, 分為兩層. 第一層為主機清單, 每台有記錄檔的主機一列, 無法開啟的
// 檔案為最後一列; 點選一列進入第二層, 即該主機的檔案清單, 一次只顯示並管理一台主機的檔案.
// 每個檔案可個別開啟於歷史畫面, 亦可選取任意多個檔案刪除; 選取, 範圍選取與刪除只作用於目前
// 所在的主機. 連線中的記錄檔可開啟, 不可刪除.
// 各主機的選取, 目前所在的主機與捲動位置於離開清單後保留, 再次進入時還原; 目前所在的主機恰好
// 只有一列選取且該列有 "開啟" 按鍵時, 把鍵盤焦點移至該按鍵. 清單重新讀取而目前所在的主機已無
// 檔案時回到第一層.
export function RecordingsPage() {
  const t = useT();
  const units = useSizeUnits();
  const [listing, setListing] = useState<RecordingListing | null>(null);
  const [failed, setFailed] = useState(false);
  const selected = useStore(recordingsListStore, (r) => r.selected);
  const storedHost = useStore(recordingsListStore, (r) => r.host);
  const [confirming, setConfirming] = useState(false);
  const contentRef = useRef<HTMLDivElement>(null);
  const soleRef = useRef<HTMLButtonElement>(null);
  const restored = useRef(false);
  // 範圍選取的起點列, 為最近一次未按 Shift 而點擊的列; 只保留於此元件存在期間.
  const anchorRef = useRef<string | null>(null);

  const reload = useCallback(async () => {
    try {
      const l = await backend.recordings();
      setListing(l);
      setFailed(false);
      const all = new Set([...l.hosts.flatMap((g) => g.files.map((f) => f.path)), ...l.skipped.map((s) => s.path)]);
      const keys = groupKeys(l);
      recordingsListStore.update((r) => ({
        ...r,
        selected: new Set([...r.selected].filter((p) => all.has(p))),
        host: r.host !== null && keys.includes(r.host) ? r.host : null,
      }));
    } catch {
      setFailed(true);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  // 離開清單時記住捲動位置.
  useLayoutEffect(() => {
    const el = contentRef.current;
    return () => {
      if (el) {
        recordingsListStore.update((r) => ({ ...r, scrollTop: el.scrollTop }));
      }
    };
  }, []);

  // 清單第一次讀取完成後還原捲動位置, 再把唯一選取且有 "開啟" 按鍵的那一列捲入可視範圍並移入焦點.
  useLayoutEffect(() => {
    if (!listing || restored.current) {
      return;
    }
    restored.current = true;
    const el = contentRef.current;
    if (el) {
      el.scrollTop = recordingsListStore.get().scrollTop;
    }
    const btn = soleRef.current;
    if (btn) {
      btn.scrollIntoView({ block: "nearest" });
      btn.focus({ preventScroll: true });
    }
  }, [listing]);

  const skipped = listing?.skipped ?? [];
  const keys = useMemo(() => (listing ? groupKeys(listing) : []), [listing]);
  // 目前所在的主機, null 為第一層; 清單讀取完成前沿用保留的值.
  const cur = !listing ? storedHost : storedHost !== null && keys.includes(storedHost) ? storedHost : null;
  const curHost = listing?.hosts.find((g) => hostKey(g.host) === cur) ?? null;
  const onSkipped = cur === skippedKey;
  // 目前所在主機的檔案, 由新到舊.
  const curFiles = useMemo(() => (curHost ? [...curHost.files].reverse() : []), [curHost]);
  const curPaths = useMemo(() => (onSkipped ? skipped.map((s) => s.path) : curFiles.map((f) => f.path)), [onSkipped, skipped, curFiles]);

  // 進入第二層, 返回第一層或因重新讀取而回到第一層時, 內容區捲回頂端並清除範圍選取的起點列.
  // 清單第一次讀取完成時不在此處理, 由下方的還原處理.
  const prevCur = useRef<string | null | undefined>(undefined);
  useLayoutEffect(() => {
    if (!listing) {
      return;
    }
    if (prevCur.current !== undefined && prevCur.current !== cur) {
      anchorRef.current = null;
      if (contentRef.current) {
        contentRef.current.scrollTop = 0;
      }
    }
    prevCur.current = cur;
  }, [listing, cur]);

  // setSelected 只改動目前所在主機的選取, 其他主機的選取不變.
  const setSelected = (fn: (sel: ReadonlySet<string>) => ReadonlySet<string>) =>
    recordingsListStore.update((r) => {
      const cur = new Set(curPaths);
      const next = fn(new Set([...r.selected].filter((p) => cur.has(p))));
      return { ...r, selected: new Set([...[...r.selected].filter((p) => !cur.has(p)), ...[...next].filter((p) => cur.has(p))]) };
    });

  // goTo 進入 key 那台主機的第二層, null 為返回第一層.
  const goTo = (key: string | null) => recordingsListStore.update((r) => ({ ...r, host: key }));

  // selectedCount 為一台主機選取的檔案數, 顯示於第一層該列的主機名稱之後.
  const selectedCount = (paths: string[]): number => paths.filter((p) => selected.has(p)).length;

  // 目前所在主機恰好只有一列選取且該列有 "開啟" 按鍵時, 為該列的路徑.
  const selectedPaths = curPaths.filter((p) => selected.has(p));
  const solePath = selectedPaths.length === 1 && !onSkipped ? selectedPaths[0] : null;

  const deletable = onSkipped ? skipped.filter((s) => selected.has(s.path)) : curFiles.filter((f) => selected.has(f.path) && !f.active);
  const deletableSize = deletable.reduce((n, f) => n + f.size, 0);

  const toggle = (path: string, on: boolean) =>
    setSelected((sel) => {
      const next = new Set(sel);
      if (on) {
        next.add(path);
      } else {
        next.delete(path);
      }
      return next;
    });

  // rangeTo 回傳起點列至 path 之間 (含兩端) 的所有列, 只在目前所在主機的列中計算; 沒有起點列或起點列不在其中時回傳 null.
  const rangeTo = (path: string): string[] | null => {
    const from = anchorRef.current === null ? -1 : curPaths.indexOf(anchorRef.current);
    const to = curPaths.indexOf(path);
    if (from < 0 || to < 0) {
      return null;
    }
    return curPaths.slice(Math.min(from, to), Math.max(from, to) + 1);
  };

  // clickRow 處理點擊列 (方框範圍以外): 一般點擊只選取該列, Ctrl (macOS 為 Command) 切換該列,
  // Shift 以起點列至該列的範圍取代目前所在主機的選取, 兩者並用則把範圍加入原有選取.
  const clickRow = (path: string, e: MouseEvent) => {
    const mod = isMac ? e.metaKey : e.ctrlKey;
    const range = e.shiftKey ? rangeTo(path) : null;
    if (range) {
      setSelected((sel) => new Set(mod ? [...sel, ...range] : range));
      return;
    }
    anchorRef.current = path;
    if (mod) {
      toggle(path, !selected.has(path));
    } else {
      setSelected(() => new Set([path]));
    }
  };

  // clickBox 處理點擊方框範圍: 切換該列; 按住 Shift 時把起點列至該列的範圍加入選取.
  const clickBox = (path: string, e: MouseEvent) => {
    const range = e.shiftKey ? rangeTo(path) : null;
    if (range) {
      setSelected((sel) => new Set([...sel, ...range]));
      return;
    }
    anchorRef.current = path;
    toggle(path, !selected.has(path));
  };

  // openFile 把該檔案所屬主機 (即目前所在的主機) 的選取改為只有該列, 再開啟檔案.
  const openFile = (path: string) => {
    setSelected(() => new Set([path]));
    openRecording(path);
  };

  const deleteGroups =
    curHost && deletable.length > 0
      ? [
          {
            host: curHost.host,
            files: curFiles
              .filter((f) => selected.has(f.path) && !f.active)
              .map((f) => ({ path: f.path, range: rangeText(f, t), size: formatSize(f.size, units, t) })),
          },
        ]
      : [];
  const deleteSkipped = onSkipped ? skipped.filter((s) => selected.has(s.path)).map((s) => ({ path: s.path, size: formatSize(s.size, units, t) })) : [];

  const doDelete = async () => {
    setConfirming(false);
    const paths = deletable.map((f) => f.path);
    try {
      const results = await backend.deleteRecordings(paths);
      for (const r of results.filter((x) => x.error)) {
        reportOperationFailure(baseName(r.path), { code: r.code || "failed", message: r.error });
      }
    } catch (err) {
      reportOperationFailure("", serviceError(err));
    }
    await reload();
  };

  return (
    <main className="main">
      <div className="toolbar">
        {cur === null ? (
          <span className="toolbar-title">{t("sidebar.recordings")}</span>
        ) : (
          <>
            <button type="button" className="btn btn-ghost" onClick={() => goTo(null)}>
              <ArrowLeftIcon />
              {t("recordings.allHosts")}
            </button>
            <span className={`toolbar-title ellipsis${onSkipped ? "" : " mono"}`}>
              {onSkipped ? t("recordings.skippedTitle") : cur.slice(2)}
            </span>
            <span className="flex1" />
            <button type="button" className="btn" disabled={deletable.length === 0} onClick={() => setConfirming(true)}>
              <DeleteIcon />
              {deletable.length > 0 ? t("recordings.deleteCount", { count: deletable.length }) : t("action.delete")}
            </button>
          </>
        )}
      </div>
      <div className="content" ref={contentRef}>
        {failed && <div className="empty-note">{t("recordings.loadFailed")}</div>}
        {listing && keys.length === 0 && <div className="empty-note">{t("recordings.empty")}</div>}
        {listing && cur === null && keys.length > 0 && (
          <section className="host-block">
            <ul className="rec-list rec-hosts">
              {listing.hosts.map((g) => (
                <HostRow
                  key={g.host}
                  label={g.host}
                  mono
                  selectedText={selectedCountText(selectedCount(g.files.map((f) => f.path)), t)}
                  files={fileCount(g.files.length, t)}
                  size={formatSize(g.total_size, units, t)}
                  onOpen={() => goTo(hostKey(g.host))}
                />
              ))}
              {skipped.length > 0 && (
                <HostRow
                  label={t("recordings.skippedTitle")}
                  selectedText={selectedCountText(selectedCount(skipped.map((x) => x.path)), t)}
                  files={fileCount(skipped.length, t)}
                  size={formatSize(skipped.reduce((n, x) => n + x.size, 0), units, t)}
                  onOpen={() => goTo(skippedKey)}
                />
              )}
            </ul>
          </section>
        )}
        {curHost && (
          <section className="host-block">
            <header className="rec-head">
              <GroupCheck
                paths={curPaths}
                selected={selected}
                label={t("recordings.selectHost", { host: curHost.host })}
                onChange={setSelected}
              />
              <span className="rec-host">{curHost.host}</span>
              <span className="flex1" />
              <span className="rec-summary">
                {t("recordings.summary", { files: fileCount(curHost.files.length, t), size: formatSize(curHost.total_size, units, t) })}
              </span>
            </header>
            <ul className="rec-list">
              {curFiles.map((f) => (
                <Row
                  key={f.path}
                  selected={selected.has(f.path)}
                  onRowClick={(e) => clickRow(f.path, e)}
                  onBoxClick={(e) => clickBox(f.path, e)}
                  muted={f.active}
                  label={rangeText(f, t)}
                  note={f.active ? t("recordings.active") : f.has_data ? undefined : t("recordings.noData")}
                  duration={f.has_data ? formatDuration(f.end - f.start, t) : undefined}
                  size={formatSize(f.size, units, t)}
                  onOpen={() => openFile(f.path)}
                  openText={t("recordings.open")}
                  openRef={f.path === solePath ? soleRef : undefined}
                />
              ))}
            </ul>
          </section>
        )}
        {onSkipped && (
          <section className="host-block">
            <header className="rec-head">
              <GroupCheck paths={curPaths} selected={selected} label={t("recordings.selectSkipped")} onChange={setSelected} />
              <span className="rec-title">{t("recordings.skippedTitle")}</span>
            </header>
            <ul className="rec-list">
              {skipped.map((s) => (
                <Row
                  key={s.path}
                  selected={selected.has(s.path)}
                  onRowClick={(e) => clickRow(s.path, e)}
                  onBoxClick={(e) => clickBox(s.path, e)}
                  label={s.path}
                  note={s.error}
                  size={formatSize(s.size, units, t)}
                />
              ))}
            </ul>
          </section>
        )}
      </div>
      {confirming && (
        <DeleteRecordingsDialog
          count={deletable.length}
          size={formatSize(deletableSize, units, t)}
          groups={deleteGroups}
          skipped={deleteSkipped}
          onCancel={() => setConfirming(false)}
          onConfirm={() => void doDelete()}
        />
      )}
    </main>
  );
}
