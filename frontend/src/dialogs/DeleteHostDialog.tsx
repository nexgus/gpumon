import type { HostStatus } from "../api/types";
import { Dialog, DialogButtons } from "../components/Dialog";
import { useT } from "../i18n";
import { removeHost } from "../state/app";

// DeleteHostDialog 為刪除主機前的確認. 主機的記錄檔不刪除.
export function DeleteHostDialog({ host, onClose }: { host: HostStatus; onClose: () => void }) {
  const t = useT();
  const submit = () => {
    onClose();
    void removeHost(host.id);
  };
  return (
    <Dialog title={t("delete.title", { name: host.name })} onCancel={onClose} onSubmit={submit}>
      <p className="dialog-text">{t("delete.text")}</p>
      <DialogButtons>
        <button type="button" className="btn" onClick={onClose}>
          {t("action.cancel")}
        </button>
        <button type="submit" className="btn btn-danger">
          {t("action.delete")}
        </button>
      </DialogButtons>
    </Dialog>
  );
}
