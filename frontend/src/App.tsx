import { useCallback, useState } from "react";
import type { HostStatus } from "./api/types";
import { Sidebar } from "./components/Sidebar";
import { DeleteHostDialog } from "./dialogs/DeleteHostDialog";
import { ConnectErrorDialog, OperationErrorDialog } from "./dialogs/ErrorDialogs";
import { HostForm } from "./dialogs/HostForm";
import { PromptDialog } from "./dialogs/PromptDialog";
import { QuitDialog } from "./dialogs/QuitDialog";
import { UploadDialog } from "./dialogs/UploadDialog";
import { MonitorView } from "./monitor/MonitorView";
import { HistoryView } from "./history/HistoryView";
import { RecordingsPage } from "./pages/RecordingsPage";
import { SettingsPage } from "./pages/SettingsPage";
import { uiStore } from "./state/app";
import { useStore } from "./state/store";

// App 為主視窗: 左側為固定側欄, 右側為主區域, 主區域依側欄的選擇顯示監看畫面,
// 記錄檔清單, 歷史畫面或設定頁面.
export default function App() {
  const page = useStore(uiStore, (u) => u.page);
  const [form, setForm] = useState<{ host: HostStatus | null } | null>(null);
  const [deleting, setDeleting] = useState<HostStatus | null>(null);
  const onAdd = useCallback(() => setForm({ host: null }), []);
  const onEdit = useCallback((h: HostStatus) => setForm({ host: h }), []);
  const onDelete = useCallback((h: HostStatus) => setDeleting(h), []);

  return (
    <div className="app">
      <Sidebar onAdd={onAdd} onEdit={onEdit} onDelete={onDelete} />
      {page === "monitor" && <MonitorView />}
      {page === "recordings" && <RecordingsPage />}
      {page === "history" && <HistoryView />}
      {page === "settings" && <SettingsPage />}
      {form && <HostForm host={form.host} onClose={() => setForm(null)} />}
      {deleting && <DeleteHostDialog host={deleting} onClose={() => setDeleting(null)} />}
      <PromptDialog />
      <UploadDialog />
      <ConnectErrorDialog />
      <OperationErrorDialog />
      <QuitDialog />
    </div>
  );
}
