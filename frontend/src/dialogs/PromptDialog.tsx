// 登入密碼與金鑰密碼的輸入視窗. 安全規定 (規格第 2 章第 7 條與 6.3):
// - 輸入欄為密碼型態, 不使用 React 的受控狀態, 輸入的值只存在於 input 元素內.
// - 送出時才讀取一次並立即交給後端, 隨即清空輸入欄; 取消, 關閉或元件卸載時亦清空.
// - 值不寫入任何狀態, 儲存區, 檔案, console 或日誌.
import { useEffect, useRef } from "react";
import { backend } from "../api/backend";
import type { Prompt } from "../api/types";
import { Dialog, DialogButtons } from "../components/Dialog";
import { WarningIcon } from "../components/Icons";
import { useT } from "../i18n";
import { modalQueueStore, promptsStore } from "../state/app";
import { useStore } from "../state/store";

function clear(input: HTMLInputElement | null): void {
  if (input) {
    input.value = "";
  }
}

function PromptForm({ prompt }: { prompt: Prompt }) {
  const t = useT();
  const input = useRef<HTMLInputElement>(null);
  const done = () => promptsStore.update((list) => list.filter((p) => p.id !== prompt.id));

  useEffect(() => {
    const el = input.current;
    return () => clear(el);
  }, []);

  const submit = () => {
    const el = input.current;
    if (!el) {
      return;
    }
    void backend.answerPrompt(prompt.id, el.value).catch(() => undefined);
    clear(el);
    done();
  };
  const cancel = () => {
    clear(input.current);
    void backend.cancelPrompt(prompt.id).catch(() => undefined);
    done();
  };

  const password = prompt.kind === "password";
  const target = prompt.user ? `${prompt.user}@${prompt.server}` : prompt.server || prompt.name;
  return (
    <Dialog title={t(password ? "prompt.passwordTitle" : "prompt.passphraseTitle")} onCancel={cancel} onSubmit={submit}>
      <p className="dialog-text">
        {password
          ? t.rich("prompt.passwordText", { target: <span className="mono strong">{target}</span> })
          : t.rich("prompt.passphraseText", { key: <span className="mono strong">{prompt.key_file}</span> })}
      </p>
      <div className="field">
        <label htmlFor="prompt-secret" className="field-label">
          {t(password ? "prompt.passwordLabel" : "prompt.passphraseLabel")}
        </label>
        <input
          id="prompt-secret"
          ref={input}
          className="input mono"
          type="password"
          autoComplete="off"
          spellCheck={false}
          defaultValue=""
        />
        <span className="field-hint">{t(password ? "prompt.passwordHint" : "prompt.passphraseHint")}</span>
      </div>
      {prompt.attempt > 1 && (
        <span className="field-error" role="alert">
          <WarningIcon />
          {t(password ? "prompt.passwordRetry" : "prompt.passphraseRetry")}
        </span>
      )}
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

// PromptDialog 依序顯示待回覆的輸入要求, 一次一個, 並與其他連線相關的視窗輪流顯示
// (見 state/app.ts 的 modalQueueStore). 收到 monitor:prompt-closed 時該要求自佇列
// 移除, 對應的視窗隨之關閉, 卸載時清空輸入欄.
export function PromptDialog() {
  const head = useStore(modalQueueStore, (q) => q[0]);
  const prompt = useStore(promptsStore, (list) => list.find((p) => `prompt:${p.id}` === head));
  if (!prompt) {
    return null;
  }
  return <PromptForm key={prompt.id} prompt={prompt} />;
}
