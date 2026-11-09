import type { HostStatus } from "../api/types";
import type { Translator } from "../i18n";

// statusText 回傳主機的狀態文字. 重新連線中顯示已重試的次數與斷線原因; 自動重新
// 連線失敗而轉為未連線時顯示斷線原因.
export function statusText(h: HostStatus, t: Translator): string {
  switch (h.state) {
    case "connected":
      return t("state.connected");
    case "connecting":
      return t("state.connecting");
    case "reconnecting":
      if (h.retries > 0) {
        return t("state.reconnectingRetry", { retry: h.retries, max: h.max_retries, reason: h.reason });
      }
      return h.reason ? t("state.reconnectingReason", { reason: h.reason }) : t("state.reconnecting");
  }
  return h.reason ? t("state.disconnectedRetries", { max: h.max_retries, reason: h.reason }) : t("state.disconnected");
}

// statusClass 回傳狀態文字的色彩 class.
export function statusClass(h: HostStatus): string {
  return `status-${h.state}`;
}

// canEdit 回報主機是否可編輯或刪除; 只有未連線的主機可以.
export function canEdit(h: HostStatus): boolean {
  return h.state === "disconnected";
}
