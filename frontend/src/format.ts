// 數值與時刻的格式. 日期與時間的格式不隨語言變動.
import type { Translator } from "./i18n";

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

// formatClock 以本機時區把 Unix 微秒格式化為 HH:MM:SS.
export function formatClock(us: number): string {
  const d = new Date(us / 1000);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

// formatClockShort 格式化為 HH:MM, 用於長的顯示範圍的刻度.
export function formatClockShort(us: number): string {
  const d = new Date(us / 1000);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// formatDateTime 格式化為 YYYY-MM-DD HH:MM:SS.
export function formatDateTime(us: number): string {
  const d = new Date(us / 1000);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${formatClock(us)}`;
}

const GiB = 1024 ** 3;
const MiB = 1024 ** 2;

// gib 回傳以 GiB 表示的數值字串, 取一位小數.
export function gib(bytes: number): string {
  return (bytes / GiB).toFixed(1);
}

// bytesUnit 依大小選擇 GiB 或 MiB, 回傳數值字串與單位.
export function bytesUnit(bytes: number): { value: string; unit: "gib" | "mib" } {
  if (bytes >= GiB) {
    return { value: gib(bytes), unit: "gib" };
  }
  return { value: String(Math.round(bytes / MiB)), unit: "mib" };
}

// formatSize 把檔案大小格式化為 KB, MB 或 GB (皆以 1024 進位), 小於 0.1 MB 用 KB,
// 達 1 GB 用 GB; MB 與 GB 取一位小數.
export function formatSize(bytes: number, t: Translator): string {
  if (bytes < MiB / 10) {
    return t("unit.kb", { value: Math.round(bytes / 1024) });
  }
  if (bytes >= GiB) {
    return t("unit.gb", { value: (bytes / GiB).toFixed(1) });
  }
  return t("unit.mb", { value: (bytes / MiB).toFixed(1) });
}

// formatDuration 把微秒的長度格式化為 "N min", "H h M min" 或 "D d H h"; 不足 1 小時
// 只列分鐘, 達 48 小時改列天與小時.
export function formatDuration(us: number, t: Translator): string {
  const minutes = Math.max(0, Math.floor(us / 60e6));
  if (minutes < 60) {
    return t("duration.min", { m: minutes });
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 48) {
    const m = minutes % 60;
    return m === 0 ? t("duration.hour", { h: hours }) : t("duration.hourMin", { h: hours, m });
  }
  const d = Math.floor(hours / 24);
  const h = hours % 24;
  return h === 0 ? t("duration.day", { d }) : t("duration.dayHour", { d, h });
}

// formatDateRange 格式化記錄檔的起訖時刻, 兩端為同一天時終點省略日期.
export function formatDateRange(start: number, end: number): string {
  const a = formatDateTime(start);
  const b = formatDateTime(end);
  return `${a} - ${a.slice(0, 10) === b.slice(0, 10) ? b.slice(11) : b}`;
}
