// 主機金鑰的確認視窗, 分為未知的主機與主機金鑰已變更兩種. Enter 只在未知的主機觸發
// "連線"; 主機金鑰已變更時 "更新並連線" 不是預設按鍵, 開啟時焦點在 "取消", 以免誤按而
// 接受變更的金鑰. Esc 等同 "取消". 取消與回覆後皆不顯示錯誤.
import type { HostKeyInfo, HostKeyPrompt } from "../api/types";
import { Dialog, DialogButtons } from "../components/Dialog";
import { WarningIcon } from "../components/Icons";
import { useT } from "../i18n";
import { answerHostKey, hostKeysStore, modalQueueStore } from "../state/app";
import { useStore } from "../state/store";

function KeyValue({ info }: { info: HostKeyInfo }) {
  return (
    <>
      <span className="key-type">{info.key_type}</span>
      <span>{info.fingerprint}</span>
    </>
  );
}

function HostKeyForm({ prompt }: { prompt: HostKeyPrompt }) {
  const t = useT();
  const host = <span className="mono strong">{prompt.name || prompt.host}</span>;
  const accept = () => answerHostKey(prompt.id, true);
  const cancel = () => answerHostKey(prompt.id, false);

  if (!prompt.changed) {
    return (
      <Dialog title={t("hostKey.unknownTitle")} width={500} onCancel={cancel} onSubmit={accept}>
        <p className="dialog-text">{t.rich("hostKey.unknownText", { host })}</p>
        <dl className="upload-facts">
          <dt>{t("hostKey.keyType")}</dt>
          <dd className="mono">{prompt.key.key_type}</dd>
          <dt>{t("hostKey.fingerprint")}</dt>
          <dd className="mono">{prompt.key.fingerprint}</dd>
        </dl>
        <DialogButtons>
          <button type="button" className="btn" onClick={cancel}>
            {t("action.cancel")}
          </button>
          <button type="submit" className="btn btn-primary">
            {t("action.connect")}
          </button>
        </DialogButtons>
      </Dialog>
    );
  }
  return (
    <Dialog title={t("hostKey.changedTitle")} width={500} onCancel={cancel}>
      <div className="hostkey-warning" role="alert">
        <WarningIcon size={15} />
        <span>{t.rich("hostKey.changedWarning", { host })}</span>
      </div>
      <p className="dialog-text">{t("hostKey.changedText")}</p>
      <dl className="upload-facts">
        <dt>{t("hostKey.newFingerprint")}</dt>
        <dd className="mono key-value">
          <KeyValue info={prompt.key} />
        </dd>
        {prompt.old_keys.map((k, i) => (
          <HostKeyOld key={i} info={k} label={t("hostKey.oldFingerprint")} />
        ))}
      </dl>
      <DialogButtons>
        <button type="button" className="btn" autoFocus onClick={cancel}>
          {t("action.cancel")}
        </button>
        <button type="button" className="btn" onClick={accept}>
          {t("action.updateConnect")}
        </button>
      </DialogButtons>
    </Dialog>
  );
}

function HostKeyOld({ info, label }: { info: HostKeyInfo; label: string }) {
  return (
    <>
      <dt>{label}</dt>
      <dd className="mono key-value">
        <KeyValue info={info} />
      </dd>
    </>
  );
}

// HostKeyDialog 依收到的先後顯示主機金鑰的確認視窗, 一次一個, 並與其他連線相關的視窗
// 輪流顯示 (見 state/app.ts 的 modalQueueStore). 收到 monitor:host-key-closed 時該詢問自
// 佇列移除, 對應的視窗隨之關閉.
export function HostKeyDialog() {
  const head = useStore(modalQueueStore, (q) => q[0]);
  const prompt = useStore(hostKeysStore, (list) => list.find((k) => `hostkey:${k.id}` === head));
  if (!prompt) {
    return null;
  }
  return <HostKeyForm key={prompt.id} prompt={prompt} />;
}
