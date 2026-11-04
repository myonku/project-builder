import * as React from 'react';
import { getGroupPlan, getProjectGantt } from '@/api/planViews';
import { toApiError, type ApiError } from '@/api/client';
import type { PlanViewPayload } from '@/types/plan';
import { todayIn } from '@/utils/datetime';

/** Only checks the local day periodically; it does not poll the API. */
export function useStatisticsDay(timeZone: string | undefined, reload: () => void) {
  const [day, setDay] = React.useState(() => todayIn(timeZone));
  const dayRef = React.useRef(day);
  const callback = React.useRef(reload);
  callback.current = reload;
  React.useEffect(() => {
    const check = () => {
      const next = todayIn(timeZone);
      if (dayRef.current !== next) {
        dayRef.current = next;
        setDay(next);
        callback.current();
      }
    };
    check();
    const timer = window.setInterval(check, 60_000);
    window.addEventListener('focus', check);
    return () => { window.clearInterval(timer); window.removeEventListener('focus', check); };
  }, [timeZone]);
  return day;
}

/** A response belongs to its exact scope and reload generation, including before effects run. */
export function useStatisticsSnapshot(kind: 'group' | 'project', id: string | null, archived: boolean, timeZone?: string) {
  const [generation, setGeneration] = React.useState(0);
  const reload = React.useCallback(() => setGeneration(value => value + 1), []);
  const today = useStatisticsDay(timeZone, reload);
  const key = JSON.stringify([kind, id, archived, timeZone, today, generation]);
  const [result, setResult] = React.useState<{ key: string; payload: PlanViewPayload | null; error: ApiError | null; readAt: string } | null>(null);
  React.useEffect(() => {
    if (!id) return;
    const controller = new AbortController();
    void (async () => {
      try {
        const data = kind === 'group'
          ? await getGroupPlan(id, { include_archived: archived }, controller.signal)
          : await getProjectGantt(id, controller.signal).then(data => ({ group_id: null, projects: [data.project], tasks: data.tasks, summary: data.summary }));
        if (!controller.signal.aborted) setResult({ key, payload: data, error: null, readAt: new Date().toISOString() });
      } catch (error) {
        if (!controller.signal.aborted) setResult({ key, payload: null, error: toApiError(error), readAt: '' });
      }
    })();
    return () => controller.abort();
  }, [key, kind, id, archived]);
  const current = id && result?.key === key ? result : null;
  return { payload: current?.payload ?? null, error: current?.error ?? null, readAt: current?.readAt ?? '', loading: !!id && !current, reload, today };
}
