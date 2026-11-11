import { memo, useEffect, useState } from "react";
import { backend } from "../api/backend";
import type { HostStatus } from "../api/types";
import { useT } from "../i18n";
import {
  connect,
  disconnect,
  hostsStore,
  selectHost,
  showPage,
  showRecordings,
  uiStore,
} from "../state/app";
import { useStore } from "../state/store";
import { canEdit, statusClass, statusText } from "../monitor/hostText";
import { DeleteIcon, EditIcon, PlusIcon, RecordingsIcon, SettingsIcon } from "./Icons";
import { StatusIcon } from "./StatusIcon";

export interface SidebarProps {
  onAdd: () => void;
  onEdit: (h: HostStatus) => void;
  onDelete: (h: HostStatus) => void;
}

const HostItem = memo(function HostItem({
  host,
  selected,
  onEdit,
  onDelete,
}: {
  host: HostStatus;
  selected: boolean;
  onEdit: (h: HostStatus) => void;
  onDelete: (h: HostStatus) => void;
}) {
  const t = useT();
  const [busy, setBusy] = useState(false);
  const editable = canEdit(host);
  const toggle = async () => {
    const connecting = host.state === "disconnected";
    // 按下 "連線" 時切換至該主機的監看畫面, 讓使用者看到連線進度; "中斷連線" 不切換畫面.
    if (connecting) {
      selectHost(host.id);
    }
    setBusy(true);
    try {
      await (connecting ? connect(host.id) : disconnect(host.id));
    } finally {
      setBusy(false);
    }
  };
  return (
    <li className={`side-host${selected ? " selected" : ""}`}>
      <button type="button" className="side-host-name" onClick={() => selectHost(host.id)}>
        <StatusIcon state={host.state} />
        <span className="ellipsis">{host.name}</span>
      </button>
      <div className={`side-host-status ${statusClass(host)}`}>{statusText(host, t)}</div>
      <div className="side-host-actions">
        {host.state === "disconnected" ? (
          <button type="button" className="btn btn-sm btn-primary" disabled={busy} onClick={toggle}>
            {t("action.connect")}
          </button>
        ) : (
          <button type="button" className="btn btn-sm" disabled={busy} onClick={toggle}>
            {t("action.disconnect")}
          </button>
        )}
        <span className="flex1" />
        <button
          type="button"
          className="btn btn-icon"
          disabled={!editable}
          aria-label={t("action.editHost", { name: host.name })}
          title={t("action.editHost", { name: host.name })}
          onClick={() => onEdit(host)}
        >
          <EditIcon />
        </button>
        <button
          type="button"
          className="btn btn-icon"
          disabled={!editable}
          aria-label={t("action.deleteHost", { name: host.name })}
          title={t("action.deleteHost", { name: host.name })}
          onClick={() => onDelete(host)}
        >
          <DeleteIcon />
        </button>
      </div>
    </li>
  );
});

export function Sidebar({ onAdd, onEdit, onDelete }: SidebarProps) {
  const t = useT();
  const hosts = useStore(hostsStore, (h) => h);
  const page = useStore(uiStore, (u) => u.page);
  const selected = useStore(uiStore, (u) => u.selected);
  const [version, setVersion] = useState("");
  useEffect(() => {
    backend.about().then(
      (info) => setVersion(`v${info.version}`),
      () => setVersion(""),
    );
  }, []);
  return (
    <nav className="sidebar" aria-label={t("sidebar.hosts")}>
      <div className="sidebar-title">{t("app.name")}</div>
      <ul className="side-hosts">
        {hosts.map((h) => (
          <HostItem
            key={h.id}
            host={h}
            selected={page === "monitor" && selected === h.id}
            onEdit={onEdit}
            onDelete={onDelete}
          />
        ))}
      </ul>
      <div className="side-add">
        <button type="button" className="btn btn-sm" onClick={onAdd}>
          <PlusIcon />
          {t("sidebar.addHost")}
        </button>
      </div>
      <div className="flex1" />
      <div className="side-nav">
        <button
          type="button"
          className={`side-nav-item${page === "recordings" || page === "history" ? " selected" : ""}`}
          onClick={showRecordings}
        >
          <RecordingsIcon />
          {t("sidebar.recordings")}
        </button>
        <button
          type="button"
          className={`side-nav-item${page === "settings" ? " selected" : ""}`}
          onClick={() => showPage("settings")}
        >
          <SettingsIcon />
          {t("sidebar.settings")}
        </button>
      </div>
      {version !== "" && (
        <div className="side-version mono ellipsis" title={version}>
          {version}
        </div>
      )}
    </nav>
  );
}
