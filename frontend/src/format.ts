// 數值與時刻的格式. 日期與時間的格式不隨語言變動.
import type { SizeUnits } from "./api/types";
import type { MessageKey, Translator } from "./i18n";

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

// base 回傳單位制的進位基數, 二進位為 1024, 十進位為 1000.
function base(units: SizeUnits): number {
  return units === "binary" ? 1024 : 1000;
}

type SizeStep = "kilo" | "mega" | "giga";

const unitKeys: Record<SizeUnits, Record<SizeStep, MessageKey>> = {
  binary: { kilo: "unit.kib", mega: "unit.mib", giga: "unit.gib" },
  decimal: { kilo: "unit.kb", mega: "unit.mb", giga: "unit.gb" },
};

// memGiga 回傳以 GiB (二進位) 或 GB (十進位) 表示的數值字串, 取一位小數.
export function memGiga(bytes: number, units: SizeUnits): string {
  return (bytes / base(units) ** 3).toFixed(1);
}

// memGigaKey 回傳 GiB 或 GB 的單位文字鍵, 其文字含 {value} 參數.
export function memGigaKey(units: SizeUnits): MessageKey {
  return unitKeys[units].giga;
}

// bytesUnit 依大小選擇 GiB 或 MiB (十進位為 GB 或 MB), 回傳數值字串與單位文字鍵.
export function bytesUnit(bytes: number, units: SizeUnits): { value: string; key: MessageKey } {
  const b = base(units);
  if (bytes >= b ** 3) {
    return { value: memGiga(bytes, units), key: unitKeys[units].giga };
  }
  return { value: String(Math.round(bytes / b ** 2)), key: unitKeys[units].mega };
}

// formatSize 把檔案大小格式化為 KiB, MiB 或 GiB (十進位為 kB, MB 或 GB), 小於 0.1 MiB
// (十進位為 0.1 MB) 用 KiB (kB), 達 1 GiB (GB) 用 GiB (GB); MiB (MB) 與 GiB (GB) 取一位小數.
export function formatSize(bytes: number, units: SizeUnits, t: Translator): string {
  const b = base(units);
  const keys = unitKeys[units];
  if (bytes < b ** 2 / 10) {
    return t(keys.kilo, { value: Math.round(bytes / b) });
  }
  if (bytes >= b ** 3) {
    return t(keys.giga, { value: (bytes / b ** 3).toFixed(1) });
  }
  return t(keys.mega, { value: (bytes / b ** 2).toFixed(1) });
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

// formatSpan 把顯示範圍的長度 (微秒) 格式化至分鐘, 例如 "5 min" 或 "1 h 20 min"; 不足 1 小時只列
// 分鐘, 其餘列小時與分鐘, 分鐘為零時省略. 寫法與記錄檔的長度相同.
export function formatSpan(us: number, t: Translator): string {
  const minutes = Math.max(0, Math.round(us / 60e6));
  if (minutes < 60) {
    return t("duration.min", { m: minutes });
  }
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return m === 0 ? t("duration.hour", { h }) : t("duration.hourMin", { h, m });
}

// formatLength 把微秒的長度格式化至秒, 例如 "2 m 5 s"; 不足 1 分鐘只列秒, 達 1 小時改列
// 小時與分鐘, 達 48 小時改列天與小時, 為零的末項省略. 用於所選時段的長度.
export function formatLength(us: number, t: Translator): string {
  const seconds = Math.max(0, Math.round(us / 1e6));
  if (seconds < 60) {
    return t("length.sec", { s: seconds });
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    const s = seconds % 60;
    return s === 0 ? t("length.min", { m: minutes }) : t("length.minSec", { m: minutes, s });
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 48) {
    const m = minutes % 60;
    return m === 0 ? t("length.hour", { h: hours }) : t("length.hourMin", { h: hours, m });
  }
  const d = Math.floor(hours / 24);
  const h = hours % 24;
  return h === 0 ? t("length.day", { d }) : t("length.dayHour", { d, h });
}

// formatDateRange 格式化記錄檔的起訖時刻, 兩端為同一天時終點省略日期.
export function formatDateRange(start: number, end: number): string {
  const a = formatDateTime(start);
  const b = formatDateTime(end);
  return `${a} - ${a.slice(0, 10) === b.slice(0, 10) ? b.slice(11) : b}`;
}
