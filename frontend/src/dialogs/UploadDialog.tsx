// 上傳遠端程式之前的通知視窗. 只有確認鍵, 沒有取消鍵; Esc 與點擊視窗外的區域皆不
// 關閉視窗. 按下確認鍵後該次連線才開始上傳.
import type { UploadNotice } from "../api/types";
import { Dialog, DialogButtons } from "../components/Dialog";
import { useT } from "../i18n";
import { confirmUpload, modalQueueStore, uploadsStore } from "../state/app";
import { useStore } from "../state/store";

function UploadForm({ notice }: { notice: UploadNotice }) {
  const t = useT();
  const host = <span className="mono strong">{notice.name || notice.host}</span>;
  return (
    <Dialog
      title={t(notice.update ? "upload.updateTitle" : "upload.uploadTitle")}
      width={400}
      onSubmit={() => confirmUpload(notice.id)}
    >
      <p className="dialog-text">{t.rich(notice.update ? "upload.updateText" : "upload.uploadText", { host })}</p>
      <dl className="upload-facts">
        <dt>{t("upload.path")}</dt>
        <dd className="mono">{notice.path}</dd>
        {notice.update && (
          <>
            <dt>{t("upload.remoteVersion")}</dt>
            <dd className="mono">{notice.remote_version}</dd>
            <dt>{t("upload.newVersion")}</dt>
            <dd className="mono">{notice.new_version}</dd>
          </>
        )}
      </dl>
      <DialogButtons>
        <button type="submit" className="btn btn-primary">
          {t("action.ok")}
        </button>
      </DialogButtons>
    </Dialog>
  );
}

// UploadDialog 依收到的先後顯示上傳通知, 一次一個, 並與其他連線相關的視窗輪流顯示
// (見 state/app.ts 的 modalQueueStore). 收到 monitor:upload-closed 時該通知自佇列
// 移除, 對應的視窗隨之關閉.
export function UploadDialog() {
  const head = useStore(modalQueueStore, (q) => q[0]);
  const notice = useStore(uploadsStore, (list) => list.find((n) => `upload:${n.id}` === head));
  if (!notice) {
    return null;
  }
  return <UploadForm key={notice.id} notice={notice} />;
}
