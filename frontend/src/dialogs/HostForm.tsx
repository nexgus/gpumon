import { useRef, useState } from "react";
import { backend } from "../api/backend";
import { serviceError } from "../api/errors";
import type { HostConn, HostStatus, ServiceError } from "../api/types";
import { Dialog, DialogButtons } from "../components/Dialog";
import { FolderIcon, WarningIcon } from "../components/Icons";
import { t as tNow, useT, type MessageKey } from "../i18n";
import { addHost, selectHost, updateHost } from "../state/app";

function FieldError({ text }: { text: string }) {
  return (
    <span className="field-error" role="alert">
      <WarningIcon />
      {text}
    </span>
  );
}

// HostForm 為新增與編輯主機的表單. 表單不含密碼欄位, 密碼於連線時才要求輸入. 位址欄位
// 失去焦點時查詢 ~/.ssh/config 對該位址的設定, 預填使用者尚未手動修改過的欄位.
export function HostForm({ host, onClose }: { host: HostStatus | null; onClose: () => void }) {
  const t = useT();
  const [addr, setAddr] = useState(host?.addr ?? "");
  const [port, setPort] = useState(host && host.port > 0 ? String(host.port) : "");
  const [user, setUser] = useState(host?.user ?? "");
  const [keyFile, setKeyFile] = useState(host?.key_file ?? "");
  const [identitiesOnly, setIdentitiesOnly] = useState(host?.identities_only ?? false);
  const [warnings, setWarnings] = useState<string[]>([]);
  // touched 記錄本次開啟表單以來使用者已手動修改的欄位, 預填不覆蓋這些欄位.
  const touched = useRef(new Set<"port" | "keyFile" | "identitiesOnly">());
  // lastQueried 為上次查詢的位址; 編輯表單以原位址起算, 位址未改變即不查詢.
  const lastQueried = useRef(host?.addr.trim() ?? "");
  // querySeq 用於丟棄較晚送出但較早回來的過時結果.
  const querySeq = useRef(0);
  const [error, setError] = useState<{ key: MessageKey; detail?: string; field: "addr" | "port" | "user" | "form" } | null>(
    null,
  );
  const [busy, setBusy] = useState(false);
  const editing = host !== null;

  const submit = async () => {
    const a = addr.trim();
    const u = user.trim();
    if (a === "") {
      setError({ key: "form.addressRequired", field: "addr" });
      return;
    }
    if (/[\s@]/.test(a) || /[\s@]/.test(u)) {
      setError({ key: "form.noSpace", field: "addr" });
      return;
    }
    if (u === "") {
      setError({ key: "errorCode.user-required", field: "user" });
      return;
    }
    let p = 0;
    if (port.trim() !== "") {
      p = Number(port.trim());
      if (!/^\d+$/.test(port.trim()) || p < 1 || p > 65535) {
        setError({ key: "form.portInvalid", field: "port" });
        return;
      }
    }
    const conn: HostConn = { addr: a, port: p, user: u, key_file: keyFile.trim(), identities_only: identitiesOnly };
    setBusy(true);
    try {
      const st = editing ? await updateHost(host.id, conn) : await addHost(conn);
      if (!editing) {
        selectHost(st.id);
      }
      onClose();
    } catch (err) {
      const e: ServiceError = serviceError(err);
      setError({
        key: `errorCode.${e.code}`,
        detail: e.code === "user-required" || e.code === "duplicate" ? undefined : e.message,
        field: e.code === "user-required" ? "user" : "form",
      });
    } finally {
      setBusy(false);
    }
  };

  const prefill = async () => {
    const a = addr.trim();
    if (a === "" || a === lastQueried.current) {
      return;
    }
    lastQueried.current = a;
    const seq = ++querySeq.current;
    try {
      const r = await backend.sshConfigDefaults(a);
      if (seq !== querySeq.current) {
        return;
      }
      if (r.port !== 0 && !touched.current.has("port")) {
        setPort(String(r.port));
      }
      if (r.key_file !== "" && !touched.current.has("keyFile")) {
        setKeyFile(r.key_file);
      }
      if (r.identities_only_set && !touched.current.has("identitiesOnly")) {
        setIdentitiesOnly(r.identities_only);
      }
      setWarnings(r.warnings ?? []);
    } catch {
      if (seq === querySeq.current) {
        setWarnings([]);
      }
    }
  };

  const browse = async () => {
    try {
      const path = await backend.chooseKeyFile(tNow("form.chooseKey"));
      if (path) {
        touched.current.add("keyFile");
        setKeyFile(path);
      }
    } catch (err) {
      const e = serviceError(err);
      setError({ key: `errorCode.${e.code}`, detail: e.message, field: "form" });
    }
  };

  return (
    <Dialog title={t(editing ? "form.editTitle" : "form.addTitle")} onCancel={onClose} onSubmit={submit}>
      <div className="field">
        <label htmlFor="hf-addr" className="field-label">
          {t("form.address")}
        </label>
        <input id="hf-addr" className="input mono" value={addr} spellCheck={false} onChange={(e) => setAddr(e.target.value)}
          onBlur={prefill}
        />
        {!editing && <span className="field-hint">{t("form.addressHint")}</span>}
      </div>
      {error?.field === "addr" && <FieldError text={t(error.key)} />}
      <div className="field-pair">
        <div className="field">
          <label htmlFor="hf-port" className="field-label">
            {t("form.port")}
          </label>
          <input
            id="hf-port"
            className="input mono"
            value={port}
            placeholder="22"
            inputMode="numeric"
            onChange={(e) => {
              touched.current.add("port");
              setPort(e.target.value);
            }}
          />
        </div>
        <div className="field">
          <label htmlFor="hf-user" className="field-label">
            {t("form.user")}
          </label>
          <input id="hf-user" className="input mono" value={user} spellCheck={false} onChange={(e) => setUser(e.target.value)} />
        </div>
      </div>
      {(error?.field === "port" || error?.field === "user") && <FieldError text={t(error.key)} />}
      <div className="field">
        <label htmlFor="hf-key" className="field-label">
          {t("form.keyFile")}
        </label>
        <div className="field-row">
          <input
            id="hf-key"
            className="input mono"
            value={keyFile}
            placeholder={t("form.keyDefault")}
            spellCheck={false}
            onChange={(e) => {
              touched.current.add("keyFile");
              setKeyFile(e.target.value);
            }}
          />
          <button type="button" className="btn" onClick={browse}>
            <FolderIcon />
            {t("action.browse")}
          </button>
        </div>
        {!editing && <span className="field-hint">{t("form.hint")}</span>}
      </div>
      <label htmlFor="hf-identities" className="check-row">
        <input
          id="hf-identities"
          type="checkbox"
          className="check"
          checked={identitiesOnly}
          onChange={(e) => {
            touched.current.add("identitiesOnly");
            setIdentitiesOnly(e.target.checked);
          }}
        />
        <span>{t("form.identitiesOnly")}</span>
      </label>
      {error?.field === "form" && (
        <div className="form-error" role="alert">
          <WarningIcon />
          <div>
            <div>{t(error.key)}</div>
            {error.detail && <div className="form-error-detail">{error.detail}</div>}
          </div>
        </div>
      )}
      {warnings.length > 0 && (
        <div className="form-error" role="status">
          <WarningIcon />
          <div>
            <div>{t("form.sshConfigWarning")}</div>
            {warnings.map((w, i) => (
              <div key={i} className="form-error-detail">
                {w}
              </div>
            ))}
          </div>
        </div>
      )}
      <DialogButtons>
        <button type="button" className="btn" onClick={onClose}>
          {t("action.cancel")}
        </button>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {t(editing ? "action.save" : "sidebar.addHost")}
        </button>
      </DialogButtons>
    </Dialog>
  );
}
