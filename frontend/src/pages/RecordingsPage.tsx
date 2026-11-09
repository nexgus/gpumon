import { useCallback, useEffect, useMemo, useState } from "react";
import { backend } from "../api/backend";
import { serviceError } from "../api/errors";
import type { RecordingFile, RecordingListing } from "../api/types";
import { DeleteIcon } from "../components/Icons";
import { formatDateRange, formatDateTime, formatDuration, formatSize } from "../format";
import { useT, type Translator } from "../i18n";
import { forgetRecordings, openRecordings, reportOperationFailure } from "../state/app";
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
  id: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  muted?: boolean;
  label: string;
  note?: string;
  duration?: string;
  size: string;
}

function Row({ id, checked, onChange, muted, label, note, duration, size }: RowProps) {
  return (
    <li className={`rec-row${checked ? " checked" : ""}${muted ? " muted" : ""}`}>
      <input id={id} type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      <label htmlFor={id} className="rec-label" title={label}>
        {label}
      </label>
      <span className="rec-note">{note}</span>
      <span className="rec-duration">{duration}</span>
      <span className="rec-size">{size}</span>
    </li>
  );
}

// RecordingsPage 為記錄檔清單: 依主機分組, 可勾選任意多個檔案開啟於歷史畫面或刪除.
// 連線中的記錄檔可開啟, 不可刪除; 無法開啟的檔案列於清單末端, 不計入任何總計.
export function RecordingsPage() {
  const t = useT();
  const [listing, setListing] = useState<RecordingListing | null>(null);
  const [failed, setFailed] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [confirming, setConfirming] = useState(false);

  const reload = useCallback(async () => {
    try {
      const l = await backend.recordings();
      setListing(l);
      setFailed(false);
      const all = new Set([...l.hosts.flatMap((g) => g.files.map((f) => f.path)), ...l.skipped.map((s) => s.path)]);
      setSelected((sel) => new Set([...sel].filter((p) => all.has(p))));
    } catch {
      setFailed(true);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const files = useMemo(() => listing?.hosts.flatMap((g) => g.files) ?? [], [listing]);
  const skipped = listing?.skipped ?? [];
  const totalSize = files.reduce((n, f) => n + f.size, 0);
  const everyPath = useMemo(() => [...files.map((f) => f.path), ...skipped.map((s) => s.path)], [files, skipped]);

  const openable = files.filter((f) => selected.has(f.path));
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

  const allSelected = everyPath.length > 0 && everyPath.every((p) => selected.has(p));

  const open = () => {
    // 歷史畫面的區塊依主機的順序排列, 同一台主機的檔案依起始時刻由早到晚.
    const order = files.filter((f) => selected.has(f.path)).map((f) => f.path);
    openRecordings(order);
  };

  const doDelete = async () => {
    setConfirming(false);
    const paths = deletable.map((f) => f.path);
    try {
      const results = await backend.deleteRecordings(paths);
      const removed = results.filter((r) => !r.error).map((r) => r.path);
      forgetRecordings(removed);
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
          onClick={() => setSelected(allSelected ? new Set() : new Set(everyPath))}
        >
          {t("recordings.selectAll")}
        </button>
        <button type="button" className="btn btn-primary" disabled={openable.length === 0} onClick={open}>
          {openable.length > 0 ? t("recordings.openCount", { count: openable.length }) : t("recordings.open")}
        </button>
        <button type="button" className="btn" disabled={deletable.length === 0} onClick={() => setConfirming(true)}>
          <DeleteIcon />
          {deletable.length > 0 ? t("recordings.deleteCount", { count: deletable.length }) : t("action.delete")}
        </button>
      </div>
      <div className="content">
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
                  id={`rec-${f.path}`}
                  checked={selected.has(f.path)}
                  onChange={(on) => toggle(f.path, on)}
                  muted={f.active}
                  label={rangeText(f, t)}
                  note={f.active ? t("recordings.active") : f.has_data ? undefined : t("recordings.noData")}
                  duration={f.has_data ? formatDuration(f.end - f.start, t) : undefined}
                  size={formatSize(f.size, t)}
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
                  id={`rec-${s.path}`}
                  checked={selected.has(s.path)}
                  onChange={(on) => toggle(s.path, on)}
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
          onCancel={() => setConfirming(false)}
          onConfirm={() => void doDelete()}
        />
      )}
    </main>
  );
}
