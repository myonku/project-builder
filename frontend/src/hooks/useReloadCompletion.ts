import * as React from 'react';

/** Existing reload stays fire-and-forget. Writes can await their own read generation. */
export function useReloadCompletion() {
  const [token, setToken] = React.useState(0);
  const next = React.useRef(0);
  const pending = React.useRef(new Map<number, (ok: boolean) => void>());
  const reload = React.useCallback(() => { setToken(++next.current); }, []);
  const reloadAsync = React.useCallback(() => {
    const generation = ++next.current;
    return new Promise<boolean>((resolve) => {
      pending.current.set(generation, resolve);
      setToken(generation);
    });
  }, []);
  const settle = React.useCallback((generation: number, ok: boolean) => {
    for (const [key, resolve] of pending.current) {
      if (key <= generation) { pending.current.delete(key); resolve(ok); }
    }
  }, []);
  React.useEffect(() => () => settle(Infinity, false), [settle]);
  return { token, reload, reloadAsync, settle };
}
