import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type Ref } from "react";
import { backend } from "../api/backend";
import { serviceError } from "../api/errors";
import type { RecordingFile, RecordingListing } from "../api/types";
import { DeleteIcon } from "../components/Icons";
import { formatDateRange, formatDateTime, formatDuration, formatSize } from "../format";
import { useT, type Translator } from "../i18n";
import { openRecording, recordingsListStore, reportOperationFailure } from "../state/app";
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

interface RowProps {
  selected: boolean;
  onSelectOnly: () => void;
  onToggle: () => void;
  muted?: boolean;
  label: string;
  note?: string;
  duration?: string;
  size: string;
  onOpen?: () => void;
  openText?: string;
  openRef?: Ref<HTMLButtonElement>;
}

// Row 為清單的一列. 點選方框與 "開啟" 按鍵以外的位置只選取該列, 點選方框只切換該列的選取.
function Row({ selected, onSelectOnly, onToggle, muted, label, note, duration, size, onOpen, openText, openRef }: RowProps) {
  return (
    <li className={`rec-row${selected ? " checked" : ""}${muted ? " muted" : ""}`} onClick={onSelectOnly}>
      <input
        type="checkbox"
        aria-label={label}
        checked={selected}
        onClick={(e) => e.stopPropagation()}
        onChange={onToggle}
      />
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

function setSelected(fn: (sel: ReadonlySet<string>) => ReadonlySet<string>): void {
  recordingsListStore.update((r) => ({ ...r, selected: fn(r.selected) }));
}

// RecordingsPage 為記錄檔清單: 依主機分組, 每個檔案可個別開啟於歷史畫面, 亦可選取任意
// 多個檔案刪除. 連線中的記錄檔可開啟, 不可刪除; 無法開啟的檔案列於清單末端, 不計入任何總計.
// 選取與捲動位置於離開清單後保留, 再次進入時還原; 恰好只有一列選取且該列有 "開啟" 按鍵時,
// 把鍵盤焦點移至該按鍵.
export function RecordingsPage() {
  const t = useT();
  const [listing, setListing] = useState<RecordingListing | null>(null);
  const [failed, setFailed] = useState(false);
  const selected = useStore(recordingsListStore, (r) => r.selected);
  const [confirming, setConfirming] = useState(false);
  const contentRef = useRef<HTMLDivElement>(null);
  const soleRef = useRef<HTMLButtonElement>(null);
  const restored = useRef(false);

  const reload = useCallback(async () => {
    try {
      const l = await backend.recordings();
      setListing(l);
      setFailed(false);
      const all = new Set([...l.hosts.flatMap((g) => g.files.map((f) => f.path)), ...l.skipped.map((s) => s.path)]);
      recordingsListStore.update((r) => ({
        ...r,
        selected: new Set([...r.selected].filter((p) => all.has(p))),
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

  const files = useMemo(() => listing?.hosts.flatMap((g) => g.files) ?? [], [listing]);
  const skipped = listing?.skipped ?? [];
  const totalSize = files.reduce((n, f) => n + f.size, 0);
  const everyPath = useMemo(() => [...files.map((f) => f.path), ...skipped.map((s) => s.path)], [files, skipped]);

  // 恰好只有一列選取且該列有 "開啟" 按鍵時, 為該列的路徑.
  const selectedPaths = everyPath.filter((p) => selected.has(p));
  const solePath = selectedPaths.length === 1 && files.some((f) => f.path === selectedPaths[0]) ? selectedPaths[0] : null;

  const deletable = [...files.filter((f) => selected.has(f.path) && !f.active), ...skipped.filter((s) => selected.has(s.path))];
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

  const selectOnly = (path: string) => setSelected(() => new Set([path]));

  const allSelected = everyPath.length > 0 && everyPath.every((p) => selected.has(p));

  const deleteGroups = (listing?.hosts ?? [])
    .map((g) => ({
      host: g.host,
      files: [...g.files]
        .reverse()
        .filter((f) => selected.has(f.path) && !f.active)
        .map((f) => ({ path: f.path, range: rangeText(f, t), size: formatSize(f.size, t) })),
    }))
    .filter((g) => g.files.length > 0);
  const deleteSkipped = skipped.filter((s) => selected.has(s.path)).map((s) => ({ path: s.path, size: formatSize(s.size, t) }));

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
        <span className="toolbar-title">{t("page.recordings")}</span>
        {listing && (
          <span className="rec-summary">
            {t("recordings.summary", { files: fileCount(files.length, t), size: formatSize(totalSize, t) })}
          </span>
        )}
        <span className="flex1" />
        <button
          type="button"
          className="btn"
          disabled={everyPath.length === 0}
          onClick={() => setSelected(() => (allSelected ? new Set() : new Set(everyPath)))}
        >
          {t("recordings.selectAll")}
        </button>
        <button type="button" className="btn" disabled={deletable.length === 0} onClick={() => setConfirming(true)}>
          <DeleteIcon />
          {deletable.length > 0 ? t("recordings.deleteCount", { count: deletable.length }) : t("action.delete")}
        </button>
      </div>
      <div className="content" ref={contentRef}>
        {failed && <div className="empty-note">{t("recordings.loadFailed")}</div>}
        {listing && files.length === 0 && skipped.length === 0 && <div className="empty-note">{t("recordings.empty")}</div>}
        {listing?.hosts.map((g) => (
          <section key={g.host} className="host-block">
            <header className="rec-head">
              <span className="rec-host">{g.host}</span>
              <span className="flex1" />
              <span className="rec-summary">
                {t("recordings.summary", { files: fileCount(g.files.length, t), size: formatSize(g.total_size, t) })}
              </span>
            </header>
            <ul className="rec-list">
              {[...g.files].reverse().map((f) => (
                <Row
                  key={f.path}
                  selected={selected.has(f.path)}
                  onSelectOnly={() => selectOnly(f.path)}
                  onToggle={() => toggle(f.path, !selected.has(f.path))}
                  muted={f.active}
                  label={rangeText(f, t)}
                  note={f.active ? t("recordings.active") : f.has_data ? undefined : t("recordings.noData")}
                  duration={f.has_data ? formatDuration(f.end - f.start, t) : undefined}
                  size={formatSize(f.size, t)}
                  onOpen={() => openRecording(f.path)}
                  openText={t("recordings.open")}
                  openRef={f.path === solePath ? soleRef : undefined}
                />
              ))}
            </ul>
          </section>
        ))}
        {skipped.length > 0 && (
          <section className="host-block">
            <header className="rec-head">
              <span className="rec-title">{t("recordings.skippedTitle")}</span>
            </header>
            <ul className="rec-list">
              {skipped.map((s) => (
                <Row
                  key={s.path}
                  selected={selected.has(s.path)}
                  onSelectOnly={() => selectOnly(s.path)}
                  onToggle={() => toggle(s.path, !selected.has(s.path))}
                  label={s.path}
                  note={s.error}
                  size={formatSize(s.size, t)}
                />
              ))}
            </ul>
          </section>
        )}
      </div>
      {confirming && (
        <DeleteRecordingsDialog
          count={deletable.length}
          size={formatSize(deletableSize, t)}
          groups={deleteGroups}
          skipped={deleteSkipped}
          onCancel={() => setConfirming(false)}
          onConfirm={() => void doDelete()}
        />
      )}
    </main>
  );
}
