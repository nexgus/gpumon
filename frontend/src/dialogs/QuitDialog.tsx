import { Dialog, DialogButtons } from "../components/Dialog";
import { useT } from "../i18n";
import { cancelQuit, confirmQuit, modalQueueStore, quitStore } from "../state/app";
import { useStore } from "../state/store";

// QuitDialog 為有主機連線中或重新連線中時結束程式前的確認視窗, 說明將中斷連線的主機數.
// 與其他連線相關的視窗輪流顯示, 見 state/app.ts 的 modalQueueStore. Esc 等同 "取消",
// Enter 等同 "結束".
export function QuitDialog() {
  const t = useT();
  const req = useStore(quitStore, (q) => q);
  const turn = useStore(modalQueueStore, (q) => q[0] === "quit");
  if (!req || !turn) {
    return null;
  }
  return (
    <Dialog title={t("quit.title")} onCancel={cancelQuit} onSubmit={confirmQuit}>
      <p className="dialog-text">
        {req.count === 1 ? t("quit.textOne") : t("quit.textMany", { count: req.count })}
      </p>
      <DialogButtons>
        <button type="button" className="btn" onClick={cancelQuit}>
          {t("action.cancel")}
        </button>
        <button type="submit" className="btn btn-primary">
          {t("action.quit")}
        </button>
      </DialogButtons>
    </Dialog>
  );
}
