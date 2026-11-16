// 免密碼登入的詢問視窗與結果視窗. 詢問視窗 Esc 等同 "暫不設定"; 結果視窗只有確認鍵,
// Esc 與 Enter 皆關閉.
import type { KeySetupPrompt } from "../api/types";
import { Dialog, DialogButtons } from "../components/Dialog";
import { useT } from "../i18n";
import { answerKeySetup, dismissKeyResult, keyResultsStore, keySetupsStore, modalQueueStore } from "../state/app";
import type { KeyResultNotice } from "../state/app";
import { useStore } from "../state/store";

function KeySetupForm({ prompt }: { prompt: KeySetupPrompt }) {
  const t = useT();
  const host = <span className="mono strong">{prompt.name || prompt.host}</span>;
  const now = () => answerKeySetup(prompt.id, "now");
  const later = () => answerKeySetup(prompt.id, "later");
  return (
    <Dialog title={t("keySetup.title")} width={420} onCancel={later} onSubmit={now}>
      <p className="dialog-text">{t.rich("keySetup.text", { host })}</p>
      <DialogButtons>
        <button type="button" className="btn" onClick={() => answerKeySetup(prompt.id, "never")}>
          {t("action.dontAsk")}
        </button>
        <button type="button" className="btn" onClick={later}>
          {t("action.notNow")}
        </button>
        <button type="submit" className="btn btn-primary">
          {t("action.setUp")}
        </button>
      </DialogButtons>
    </Dialog>
  );
}

// KeySetupDialog 依收到的先後顯示免密碼登入的詢問, 一次一個, 與其他連線相關的視窗輪流
// 顯示 (見 state/app.ts 的 modalQueueStore). 收到 monitor:key-setup-closed 時該詢問自佇列
// 移除, 對應的視窗隨之關閉.
export function KeySetupDialog() {
  const head = useStore(modalQueueStore, (q) => q[0]);
  const prompt = useStore(keySetupsStore, (list) => list.find((k) => `keysetup:${k.id}` === head));
  if (!prompt) {
    return null;
  }
  return <KeySetupForm key={prompt.id} prompt={prompt} />;
}

function KeyResultForm({ result }: { result: KeyResultNotice }) {
  const t = useT();
  const close = () => dismissKeyResult(result.id);
  const host = <span className="mono strong">{result.name || result.host}</span>;
  const key =
    result.outcome === "success"
      ? "keyResult.success"
      : result.outcome === "generate-failed"
        ? "keyResult.generateFailed"
        : "keyResult.appendFailed";
  const failed = result.outcome !== "success";
  return (
    <Dialog title={t("keySetup.title")} width={420} onCancel={close} onSubmit={close}>
      <p className="dialog-text">{t.rich(key, { host })}</p>
      {result.generated && result.outcome !== "generate-failed" && (
        <p className="dialog-text">
          {t.rich("keyResult.generated", { keyFile: <span className="mono strong">{result.key_file}</span> })}
        </p>
      )}
      {failed && result.reason && <span className="error-detail">{result.reason}</span>}
      <DialogButtons>
        <button type="submit" className="btn btn-primary">
          {t("action.ok")}
        </button>
      </DialogButtons>
    </Dialog>
  );
}

// KeyResultDialog 依收到的先後顯示免密碼登入的結果, 一次一個, 與其他連線相關的視窗輪流
// 顯示 (見 state/app.ts 的 modalQueueStore).
export function KeyResultDialog() {
  const head = useStore(modalQueueStore, (q) => q[0]);
  const result = useStore(keyResultsStore, (list) => list.find((r) => `keyresult:${r.id}` === head));
  if (!result) {
    return null;
  }
  return <KeyResultForm key={result.id} result={result} />;
}
