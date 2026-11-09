// isMac 表示程式執行於 macOS, 供介面依作業系統顯示不同的按鍵名稱與操作說明.
export const isMac = /Mac/i.test(navigator.platform || navigator.userAgent);
