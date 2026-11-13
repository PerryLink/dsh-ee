import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "./api";

/** 加载一次接口数据，提供 reload；组件卸载或重新加载后，过期的响应会被丢弃。 */
export function useApi<T>(fn: () => Promise<T>) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  // 出错时的 HTTP 状态码（0=网络不可达）；调用方需要区分「没权限」和「加载失败」时用。
  const [errorStatus, setErrorStatus] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const seq = useRef(0);
  const fnRef = useRef(fn);
  fnRef.current = fn;

  const reload = useCallback(() => {
    const id = ++seq.current;
    setLoading(true);
    fnRef
      .current()
      .then((d) => {
        if (id === seq.current) {
          setData(d);
          setError(null);
          setErrorStatus(null);
        }
      })
      .catch((e: unknown) => {
        if (id === seq.current) {
          setError(e instanceof Error ? e.message : String(e));
          setErrorStatus(e instanceof ApiError ? e.status : null);
        }
      })
      .finally(() => {
        if (id === seq.current) setLoading(false);
      });
  }, []);

  useEffect(() => {
    reload();
    return () => {
      seq.current++;
    };
  }, [reload]);

  return { data, error, errorStatus, loading, reload };
}

/** 执行一个写操作，期间禁用按钮，失败时返回错误信息。 */
export function useAction() {
  const [busy, setBusy] = useState(false);
  const run = useCallback(async <T,>(fn: () => Promise<T>): Promise<{ ok: true; value: T } | { ok: false; error: string }> => {
    setBusy(true);
    try {
      return { ok: true, value: await fn() };
    } catch (e) {
      return { ok: false, error: e instanceof Error ? e.message : String(e) };
    } finally {
      setBusy(false);
    }
  }, []);
  return { busy, run };
}
