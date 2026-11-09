import type { HostState } from "../api/types";

// StatusIcon 為主機狀態圖示, 四種狀態的形狀各不相同, 見規格 6.5.
export function StatusIcon({ state }: { state: HostState }) {
  return (
    <svg aria-hidden="true" width="10" height="10" viewBox="0 0 10 10" style={{ flex: "none" }}>
      {state === "connected" && <circle cx="5" cy="5" r="4" style={{ fill: "var(--ok)" }} />}
      {state === "connecting" && (
        <circle cx="5" cy="5" r="3.5" style={{ fill: "none", stroke: "var(--accent)", strokeWidth: 2, strokeDasharray: "2 2" }} />
      )}
      {state === "reconnecting" && (
        <circle cx="5" cy="5" r="3.5" style={{ fill: "none", stroke: "var(--warn)", strokeWidth: 2, strokeDasharray: "5 3" }} />
      )}
      {state === "disconnected" && (
        <circle cx="5" cy="5" r="3.5" style={{ fill: "none", stroke: "var(--off)", strokeWidth: 1.5 }} />
      )}
    </svg>
  );
}
