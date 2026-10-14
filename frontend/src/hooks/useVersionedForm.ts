/**
 * 版本协议（`expected_updated_at`）的统一实现（F01 §5.3）。
 *
 * 后端把 `expected_updated_at` 设为**必填**（《S02》§2.6），覆盖全部 PATCH 类端点。
 * 本 Hook 负责三件事：
 * 1. `load()`：GET 拿到对象，缓存 `updated_at` 的**原字符串**；
 * 2. `save(patch)`：只提交用户实际修改的字段 + `expected_updated_at`；
 * 3. 收到 `409 version_conflict` 时**不自动重试、不静默合并**，弹窗让用户选
 *    「放弃我的修改并刷新」或「保留我的修改」。
 *
 * **不要**在调用方手工拼 `expected_updated_at`——那是文档 F01 §6 第 1 条禁止的用法。
 */

import * as React from 'react';
import { ApiError, toApiError } from '@/api/client';
import { useDialogs } from '@/hooks/useDialogs';

export type SaveOutcome<TEntity> =
  | { ok: true; entity: TEntity }
  | {
      ok: false;
      error: ApiError;
      /**
       * `reloaded`：用户选择放弃修改，已重新加载服务端最新数据；
       * `kept`：用户选择保留修改，本地版本号已刷新，可再次提交；
       * `aborted`：非版本冲突的失败，或本地没有版本号可用。
       */
      resolution: 'reloaded' | 'kept' | 'aborted';
    };

/** `*Body` 类型都至少包含 `expected_updated_at` */
export interface VersionedBody {
  readonly expected_updated_at: string;
}

export interface UseVersionedFormOptions<
  TEntity,
  TBody extends VersionedBody,
> {
  /** 拉取对象（通常直接传 `() => getProject(id)`） */
  load: (signal?: AbortSignal) => Promise<TEntity>;
  /** 提交更新（通常直接传 `(body) => updateProject(id, body)`） */
  save: (body: TBody) => Promise<TEntity>;
  /** 从实体取出版本号；默认读 `entity.updated_at` */
  versionOf?: (entity: TEntity) => string;
  /** 挂载后自动加载，默认 true */
  autoLoad?: boolean;
  /** 并发冲突弹窗的标题 */
  conflictTitle?: string;
}

export interface VersionedFormState<TEntity, TBody extends VersionedBody> {
  entity: TEntity | null;
  /** 服务端 `updated_at` 的**原样**字符串 */
  version: string | null;
  loading: boolean;
  saving: boolean;
  error: ApiError | null;
  load: (signal?: AbortSignal) => Promise<TEntity | null>;
  reload: () => Promise<TEntity | null>;
  /** 只提交用户实际修改的字段；版本号由本 Hook 注入 */
  save: (patch: Omit<TBody, 'expected_updated_at'>) => Promise<SaveOutcome<TEntity>>;
  /** 本地替换实体（例如表单提交成功后合并返回值） */
  setEntity: (entity: TEntity | null) => void;
}

function defaultVersionOf<TEntity>(entity: TEntity): string {
  const value = (entity as { updated_at?: unknown } | null)?.updated_at;
  return typeof value === 'string' ? value : '';
}

export function useVersionedForm<TEntity, TBody extends VersionedBody>(
  options: UseVersionedFormOptions<TEntity, TBody>,
): VersionedFormState<TEntity, TBody> {
  const {
    load: loadFn,
    save: saveFn,
    versionOf = defaultVersionOf,
    autoLoad = true,
    conflictTitle = '检测到并发修改',
  } = options;

  const dialogs = useDialogs();
  const [entity, setEntity] = React.useState<TEntity | null>(null);
  const [version, setVersion] = React.useState<string | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState<ApiError | null>(null);

  // 用 ref 固定最新的回调，避免把调用方每次渲染新建的函数放进 effect 依赖
  const loadRef = React.useRef(loadFn);
  loadRef.current = loadFn;
  const saveRef = React.useRef(saveFn);
  saveRef.current = saveFn;
  const versionOfRef = React.useRef(versionOf);
  versionOfRef.current = versionOf;

  const load = React.useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError(null);
    try {
      const loaded = await loadRef.current(signal);
      setEntity(loaded);
      setVersion(versionOfRef.current(loaded));
      return loaded;
    } catch (caught) {
      const apiError = toApiError(caught);
      if (apiError.code !== 'aborted') {
        setError(apiError);
      }
      return null;
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    if (!autoLoad) {
      return undefined;
    }
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [autoLoad, load]);

  const save = React.useCallback(
    async (patch: Omit<TBody, 'expected_updated_at'>): Promise<SaveOutcome<TEntity>> => {
      if (version === null) {
        return {
          ok: false,
          error: new ApiError({
            status: 0,
            code: 'version_unavailable',
            message: '本地还没有对象版本号，请先加载对象。',
          }),
          resolution: 'aborted',
        };
      }
      setSaving(true);
      setError(null);
      const body = { ...patch, expected_updated_at: version } as TBody;
      try {
        const saved = await saveRef.current(body);
        setEntity(saved);
        setVersion(versionOfRef.current(saved));
        return { ok: true, entity: saved };
      } catch (caught) {
        const apiError = toApiError(caught);
        setError(apiError);
        if (!apiError.isVersionConflict) {
          return { ok: false, error: apiError, resolution: 'aborted' };
        }

        const previousEntity = entity;
        const requestHint = apiError.requestId ? `（request_id: ${apiError.requestId}）` : '';
        const discard = await dialogs.confirm(
          `这个对象在你编辑期间已被其他人修改。可以放弃你的修改并加载最新数据，` +
            `也可以保留你的修改、刷新版本号后再提交一次。${requestHint}`,
          {
            title: conflictTitle,
            okText: '放弃我的修改并刷新',
            cancelText: '保留我的修改',
            severity: 'warning',
          },
        );

        if (discard) {
          await load();
          return { ok: false, error: apiError, resolution: 'reloaded' };
        }

        // 保留用户修改：只把版本号换成服务端最新值，**不动** entity 里的编辑内容
        const fresh = await loadRef.current();
        setEntity(previousEntity);
        setVersion(versionOfRef.current(fresh));
        return { ok: false, error: apiError, resolution: 'kept' };
      } finally {
        setSaving(false);
      }
    },
    [dialogs, entity, load, version],
  );

  const reload = React.useCallback(() => load(), [load]);

  return {
    entity,
    version,
    loading,
    saving,
    error,
    load,
    reload,
    save,
    setEntity,
  };
}
