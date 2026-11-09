import type { ErrorCode, ServiceError } from "./types";

const codes: ErrorCode[] = ["invalid", "not-found", "duplicate", "user-required", "busy", "active", "failed", "closed"];

// serviceError 由服務方法 rejected 的錯誤取出 {code, message}. 錯誤不含後端的 cause
// 時 (例如程式內部錯誤) 視為 failed, message 取錯誤本身的說明.
export function serviceError(err: unknown): ServiceError {
  const e = err as { cause?: unknown; message?: unknown } | null;
  const cause = e?.cause as { code?: unknown; message?: unknown } | undefined;
  if (cause && typeof cause.code === "string" && codes.includes(cause.code as ErrorCode)) {
    return { code: cause.code as ErrorCode, message: typeof cause.message === "string" ? cause.message : "" };
  }
  const message = typeof e?.message === "string" ? e.message : String(err);
  return { code: "failed", message };
}
