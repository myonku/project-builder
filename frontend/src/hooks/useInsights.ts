import * as React from 'react';
import { toApiError, type ApiError } from '@/api/client';
import { useStatisticsDay } from './useStatisticsSnapshot';
/** Scope-keyed reads clear errors/stale values immediately, including before the effect runs. */
export function useInsights<T>(query: object | null, load: (signal: AbortSignal) => Promise<T>, timezone?: string) {
 const [generation, setGeneration] = React.useState(0);
 const reload = React.useCallback(() => setGeneration(v => v + 1), []);
 const today = useStatisticsDay(timezone, reload);
 const key = JSON.stringify([query, generation, today]);
 const loader = React.useRef(load); loader.current = load;
 const [state, setState] = React.useState<{key: string; data: T | null; error: ApiError | null} | null>(null);
 React.useEffect(() => {
  if (!query) return;
  const controller = new AbortController();
  const read = loader.current;
  // Strict Mode 会立即清理并重新运行 effect；在微任务中启动可避免发出注定取消的请求。
  void (async () => {
   await Promise.resolve();
   if (controller.signal.aborted) return;
   try {
    const data = await read(controller.signal);
    if (!controller.signal.aborted) setState({key, data, error: null});
   } catch (error) {if (!controller.signal.aborted) setState({key, data: null, error: toApiError(error)});}
  })();
  return () => controller.abort();
 // Content rather than callback identity determines the request.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 }, [key]);
 const current = query && state?.key === key ? state : null;
 return {data: current?.data ?? null, error: current?.error ?? null, loading: !!query && !current, reload, today};
}
