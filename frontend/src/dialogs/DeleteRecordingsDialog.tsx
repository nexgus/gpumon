import { Dialog, DialogButtons } from "../components/Dialog";
import { useT } from "../i18n";

export interface DeleteGroup {
  host: string;
  files: { path: string; range: string; size: string }[];
}

// DeleteRecordingsDialog 為刪除記錄檔前的確認, 依主機分組列出將刪除的檔案, 並顯示將釋出的大小.
// 無法開啟的檔案列於最後一組.
export function DeleteRecordingsDialog({
  count,
  size,
  groups,
  skipped,
  onCancel,
  onConfirm,
}: {
  count: number;
  size: string;
  groups: DeleteGroup[];
  skipped: { path: string; size: string }[];
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const t = useT();
  return (
    <Dialog
      title={count === 1 ? t("recordings.deleteTitleOne") : t("recordings.deleteTitle", { count })}
      width={440}
      onCancel={onCancel}
      onSubmit={onConfirm}
    >
      <div className="del-list">
        {groups.map((g) => (
          <div key={g.host}>
            <div className="del-host">{g.host}</div>
            <ul className="del-files">
              {g.files.map((f) => (
                <li key={f.path} className="del-file">
                  <span className="del-range">{f.range}</span>
                  <span className="del-size">{f.size}</span>
                </li>
              ))}
            </ul>
          </div>
        ))}
        {skipped.length > 0 && (
          <div>
            <div className="del-host">{t("recordings.skippedTitle")}</div>
            <ul className="del-files">
              {skipped.map((s) => (
                <li key={s.path} className="del-file">
                  <span className="del-range wrap">{s.path}</span>
                  <span className="del-size">{s.size}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
      <p className="dialog-text">{t("recordings.deleteText", { size })}</p>
      <DialogButtons>
        <button type="button" className="btn" onClick={onCancel}>
          {t("action.cancel")}
        </button>
        <button type="submit" className="btn btn-danger">
          {t("action.delete")}
        </button>
      </DialogButtons>
    </Dialog>
  );
}
