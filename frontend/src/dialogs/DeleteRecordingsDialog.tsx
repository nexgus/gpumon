import { Dialog, DialogButtons } from "../components/Dialog";
import { useT } from "../i18n";

// DeleteRecordingsDialog 為刪除記錄檔前的確認, 列出將刪除的檔案數與將釋出的大小.
export function DeleteRecordingsDialog({
  count,
  size,
  onCancel,
  onConfirm,
}: {
  count: number;
  size: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const t = useT();
  return (
    <Dialog
      title={count === 1 ? t("recordings.deleteTitleOne") : t("recordings.deleteTitle", { count })}
      onCancel={onCancel}
      onSubmit={onConfirm}
    >
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
