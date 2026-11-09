import { Dialog, DialogButtons } from "../components/Dialog";
import { WarningIcon } from "../components/Icons";
import { useT } from "../i18n";
import {
  connectErrorsStore,
  dismissConnectErrors,
  dismissOperationErrors,
  modalQueueStore,
  operationErrorsStore,
} from "../state/app";
import { useStore } from "../state/store";

function Entry({ name, text, detail }: { name: string; text: string; detail: string }) {
  return (
    <li className="error-entry">
      <span className="error-icon">
        <WarningIcon size={15} />
      </span>
      <div className="error-body">
        {name && <span className="mono strong">{name}</span>}
        <span className="error-text">{text}</span>
        {detail && detail !== text && <span className="error-detail">{detail}</span>}
      </div>
    </li>
  );
}

// ConnectErrorDialog 為檢查出錯誤而斷線時的錯誤視窗. 同一時間只有一個, 視窗開啟期間
// 之後的錯誤附加於其中, 逐台列出. 錯誤種類以語言資源檔的說明顯示, 後端的英文說明
// 照原文列於其下. 與其他連線相關的視窗輪流顯示, 見 state/app.ts 的 modalQueueStore.
export function ConnectErrorDialog() {
  const t = useT();
  const errors = useStore(connectErrorsStore, (e) => e);
  const turn = useStore(modalQueueStore, (q) => q[0] === "connect-errors");
  if (errors.length === 0 || !turn) {
    return null;
  }
  return (
    <Dialog title={t("errors.title")} width={420} onCancel={dismissConnectErrors} onSubmit={dismissConnectErrors}>
      <ul className="error-list">
        {errors.map((e) => (
          <Entry key={e.host} name={e.name || e.host} text={t(`errorKind.${e.kind}`)} detail={e.message} />
        ))}
      </ul>
      <DialogButtons>
        <button type="submit" className="btn btn-primary">
          {t("action.close")}
        </button>
      </DialogButtons>
    </Dialog>
  );
}

// OperationErrorDialog 顯示服務方法失敗的錯誤, 例如設定檔寫入失敗.
export function OperationErrorDialog() {
  const t = useT();
  const errors = useStore(operationErrorsStore, (e) => e);
  const turn = useStore(modalQueueStore, (q) => q[0] === "operation-errors");
  if (errors.length === 0 || !turn) {
    return null;
  }
  return (
    <Dialog title={t("errors.operationTitle")} width={420} onCancel={dismissOperationErrors} onSubmit={dismissOperationErrors}>
      <ul className="error-list">
        {errors.map((e, i) => (
          <Entry key={i} name={e.name} text={t(`errorCode.${e.error.code}`)} detail={e.error.message} />
        ))}
      </ul>
      <DialogButtons>
        <button type="submit" className="btn btn-primary">
          {t("action.close")}
        </button>
      </DialogButtons>
    </Dialog>
  );
}
