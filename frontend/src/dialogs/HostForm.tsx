import { useState } from "react";
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

// HostForm 為新增與編輯主機的表單. 表單不含密碼欄位, 密碼於連線時才要求輸入.
export function HostForm({ host, onClose }: { host: HostStatus | null; onClose: () => void }) {
  const t = useT();
  const [addr, setAddr] = useState(host?.addr ?? "");
  const [port, setPort] = useState(host && host.port > 0 ? String(host.port) : "");
  const [user, setUser] = useState(host?.user ?? "");
  const [keyFile, setKeyFile] = useState(host?.key_file ?? "");
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
    let p = 0;
    if (port.trim() !== "") {
      p = Number(port.trim());
      if (!/^\d+$/.test(port.trim()) || p < 1 || p > 65535) {
        setError({ key: "form.portInvalid", field: "port" });
        return;
      }
    }
    const conn: HostConn = { addr: a, port: p, user: u, key_file: keyFile.trim() };
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

  const browse = async () => {
    try {
      const path = await backend.chooseKeyFile(tNow("form.chooseKey"));
      if (path) {
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
        <input id="hf-addr" className="input mono" value={addr} spellCheck={false} onChange={(e) => setAddr(e.target.value)} />
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
            onChange={(e) => setPort(e.target.value)}
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
            onChange={(e) => setKeyFile(e.target.value)}
          />
          <button type="button" className="btn" onClick={browse}>
            <FolderIcon />
            {t("action.browse")}
          </button>
        </div>
        {!editing && <span className="field-hint">{t("form.hint")}</span>}
      </div>
      {error?.field === "form" && (
        <div className="form-error" role="alert">
          <WarningIcon />
          <div>
            <div>{t(error.key)}</div>
            {error.detail && <div className="form-error-detail">{error.detail}</div>}
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
