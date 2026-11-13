import { useState } from "react";
import type { Device, ProcFilter } from "../api/types";
import { Dialog, DialogButtons } from "../components/Dialog";
import { WarningIcon } from "../components/Icons";
import { useT } from "../i18n";

// initialChecks 回傳開啟視窗時勾選的 GPU. 已儲存的 GPU 條件只保留屬於目前 GPU 的
// 編號; 沒有 GPU 條件, 或保留後一個也不剩時全部勾選.
function initialChecks(gpus: Device[], filter: ProcFilter | undefined): Set<number> {
  const ids = new Set(gpus.map((g) => g.id));
  const kept = (filter?.gpus ?? []).filter((id) => ids.has(id));
  return kept.length > 0 ? new Set(kept) : ids;
}

// ProcessFilterDialog 為程序卡片的過濾條件視窗. gpus 為該主機 (歷史畫面為該記錄檔)
// 的 GPU. 全部勾選代表沒有 GPU 條件; 一張也未勾選時不可套用. "Clear All" 只恢復視窗
// 內的條件, 須再按 "Apply" 才生效. onApply 收到的條件尚未去除前後空白.
export function ProcessFilterDialog({
  gpus,
  filter,
  onApply,
  onClose,
}: {
  gpus: Device[];
  filter: ProcFilter | undefined;
  onApply: (f: ProcFilter) => void;
  onClose: () => void;
}) {
  const t = useT();
  const [checked, setChecked] = useState(() => initialChecks(gpus, filter));
  const [user, setUser] = useState(filter?.user ?? "");
  const [command, setCommand] = useState(filter?.command ?? "");
  const noneChecked = gpus.length > 0 && !gpus.some((g) => checked.has(g.id));

  const toggle = (id: number, on: boolean) => {
    const next = new Set(checked);
    if (on) {
      next.add(id);
    } else {
      next.delete(id);
    }
    setChecked(next);
  };
  const clearAll = () => {
    setChecked(new Set(gpus.map((g) => g.id)));
    setUser("");
    setCommand("");
  };
  const apply = () => {
    if (noneChecked) {
      return;
    }
    const sel = gpus.filter((g) => checked.has(g.id)).map((g) => g.id);
    onApply({ gpus: sel.length === gpus.length ? [] : sel, user, command });
    onClose();
  };

  return (
    <Dialog title={t("filter.title")} width={380} onCancel={onClose} onSubmit={apply}>
      {gpus.length > 0 && (
        <div
          role="group"
          aria-labelledby="pf-gpu-label"
          className="filter-gpus"
          onKeyDown={(e) => {
            // 勾選框上按 Enter 時瀏覽器不一定送出表單, 於此一律視為套用.
            if (e.key === "Enter") {
              e.preventDefault();
              apply();
            }
          }}
        >
          <span id="pf-gpu-label" className="field-label">
            {t("filter.gpu")}
          </span>
          {gpus.map((g) => (
            <label key={g.id} htmlFor={`pf-gpu${g.id}`} className="filter-gpu">
              <input
                id={`pf-gpu${g.id}`}
                type="checkbox"
                className="check"
                checked={checked.has(g.id)}
                onChange={(e) => toggle(g.id, e.target.checked)}
              />
              <span className="gpu-badge">{t("monitor.gpu", { id: g.id })}</span>
              <span className="filter-gpu-name ellipsis" title={g.name}>
                {g.name}
              </span>
            </label>
          ))}
        </div>
      )}
      {noneChecked && (
        <span className="field-error" role="alert">
          <WarningIcon />
          {t("filter.noGpu")}
        </span>
      )}
      <div className="field">
        <label htmlFor="pf-user" className="field-label">
          {t("filter.user")}
        </label>
        <div className="field-row">
          <input
            id="pf-user"
            className="input mono"
            value={user}
            placeholder={t("filter.userAny")}
            spellCheck={false}
            autoComplete="off"
            onChange={(e) => setUser(e.target.value)}
          />
        </div>
      </div>
      <div className="field">
        <label htmlFor="pf-command" className="field-label">
          {t("filter.command")}
        </label>
        <div className="field-row">
          <input
            id="pf-command"
            className="input mono"
            value={command}
            placeholder={t("filter.commandAny")}
            spellCheck={false}
            autoComplete="off"
            onChange={(e) => setCommand(e.target.value)}
          />
        </div>
      </div>
      <span className="field-hint">{t("filter.hint")}</span>
      <DialogButtons>
        <button type="button" className="btn btn-ghost" onClick={clearAll}>
          {t("filter.clearAll")}
        </button>
        <span className="flex1" />
        <button type="button" className="btn" onClick={onClose}>
          {t("action.cancel")}
        </button>
        <button type="submit" className="btn btn-primary" disabled={noneChecked}>
          {t("filter.apply")}
        </button>
      </DialogButtons>
    </Dialog>
  );
}
