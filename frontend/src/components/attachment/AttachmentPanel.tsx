/**
 * 附件面板（项目详情 / 任务详情抽屉共用）——《A07》，F05 §7.2 第 8 步。
 *
 * 契约要点：
 * - 附件**没有独立权限**，完全继承所在项目/任务；上传与逻辑删除要求容器写权限，
 *   归档项目下统一只读（A07 `CanWriteAttachment`）。
 * - 逻辑删除（`DELETE`）只置 `deleted_at`，字节与配额仍在；**彻底删除**（`purge`，仅限已删除）
 *   才释放磁盘与配额。两步不可合并。
 * - 列表默认不显示已删除；打开“含已删除”后，服务端只把**上传者本人、容器持有者与管理员**
 *   能看到的那部分已删除行返回（A07 §端点 2 备注）。
 * - 下载是字节流，不是 JSON 信封；这里用 `downloadAttachment` 取 blob 后再触发浏览器下载，
 *   避免用 `<a href>` 把错误响应存成坏文件（F01 §5.6）。
 *
 * 布局照项目/任务成员面板的“列表 + 动作”结构；分页用轻量的上一页/下一页，
 * 不引入第二张 DataGrid（附件通常很少，塞进抽屉也不需要横向滚动）。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Link from '@mui/material/Link';
import CircularProgress from '@mui/material/CircularProgress';
import Divider from '@mui/material/Divider';
import FormControlLabel from '@mui/material/FormControlLabel';
import IconButton from '@mui/material/IconButton';
import LinearProgress from '@mui/material/LinearProgress';
import Stack from '@mui/material/Stack';
import Switch from '@mui/material/Switch';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import DeleteForeverIcon from '@mui/icons-material/DeleteForever';
import DeleteIcon from '@mui/icons-material/Delete';
import DownloadIcon from '@mui/icons-material/Download';
import InsertDriveFileIcon from '@mui/icons-material/InsertDriveFile';
import RefreshIcon from '@mui/icons-material/Refresh';
import UploadFileIcon from '@mui/icons-material/UploadFile';
import {
  deleteAttachment,
  downloadAttachment,
  purgeAttachment,
  queryProjectAttachments,
  queryTaskAttachments,
  uploadProjectAttachment,
  uploadTaskAttachment,
} from '@/api/attachments';
import { toApiError, type ApiError } from '@/api/client';
import StatusTag from '@/components/common/StatusTag';
import { truncatedText } from '@/components/common/TruncatedText';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useNotifications } from '@/hooks/useNotifications';
import { useAuthStore } from '@/stores/auth';
import type { Attachment, AttachmentQueryBody } from '@/types/attachment';
import { formatForDisplay } from '@/utils/datetime';

const AttachmentPreview = React.lazy(() => import('./AttachmentPreview'));
const PAGE_SIZE = 10;

export interface AttachmentPanelProps {
  target: { kind: 'project' | 'task'; id: string };
  /** 上传与逻辑删除权限（服务端 `CanWriteAttachment`） */
  canWrite: boolean;
  /** 容器持有者（项目/任务 owner）；系统管理员由面板内部识别 */
  canPurgeAll?: boolean;
  /** 任务抽屉里用更紧凑的小标题 */
  dense?: boolean;
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) {
    return '—';
  }
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  if (bytes < 1024 * 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

export default function AttachmentPanel({
  target,
  canWrite,
  canPurgeAll = false,
  dense = false,
}: AttachmentPanelProps) {
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const { handle } = useApiError();
  const user = useAuthStore((state) => state.user);
  const userTimeZone = user?.timezone;

  const [preview, setPreview] = React.useState<Attachment | null>(null);
  const [items, setItems] = React.useState<Attachment[]>([]);
  const [total, setTotal] = React.useState(0);
  const [page, setPage] = React.useState(0);
  const [includeDeleted, setIncludeDeleted] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<ApiError | null>(null);
  const [uploadError, setUploadError] = React.useState<ApiError | null>(null);
  const [uploading, setUploading] = React.useState(false);
  const [busyId, setBusyId] = React.useState<string | null>(null);
  const [downloadingId, setDownloadingId] = React.useState<string | null>(null);
  const [refreshToken, setRefreshToken] = React.useState(0);

  const reload = React.useCallback(() => {
    setRefreshToken((token) => token + 1);
  }, []);

  /** 上传/删除后回到第 1 页：新附件总在最前面，删除后也不容易停在空页 */
  const refreshFromFirstPage = React.useCallback(() => {
    setPage(0);
    reload();
  }, [reload]);

  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;

    setLoading(true);
    setError(null);
    const body: AttachmentQueryBody = {
      page: page + 1,
      page_size: PAGE_SIZE,
      sort: '-created_at',
    };
    if (includeDeleted) {
      body.include_deleted = true;
    }

    void (async () => {
      try {
        const result =
          target.kind === 'project'
            ? await queryProjectAttachments(target.id, body, controller.signal)
            : await queryTaskAttachments(target.id, body, controller.signal);
        if (cancelled) {
          return;
        }
        setItems(result.data ?? []);
        setTotal(result.pagination?.total ?? 0);
      } catch (caught) {
        if (cancelled) {
          return;
        }
        const apiError = toApiError(caught);
        if (apiError.code !== 'aborted') {
          setError(apiError);
          setItems([]);
          setTotal(0);
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    })();

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [target.kind, target.id, page, includeDeleted, refreshToken]);

  const handleUpload = React.useCallback(
    async (file: File) => {
      setUploading(true);
      setUploadError(null);
      try {
        if (target.kind === 'project') {
          await uploadProjectAttachment(target.id, file);
        } else {
          await uploadTaskAttachment(target.id, file);
        }
        show(`附件「${file.name}」已上传。`, { severity: 'success' });
        refreshFromFirstPage();
      } catch (caught) {
        const apiError = toApiError(caught);
        // 401 仍走统一跳转；403/413/415/409/422 的细节留在面板内展示
        handle(apiError, { silent: true });
        setUploadError(apiError);
      } finally {
        setUploading(false);
      }
    },
    [handle, refreshFromFirstPage, show, target.id, target.kind],
  );

  const handleDelete = React.useCallback(
    async (attachment: Attachment) => {
      const confirmed = await dialogs.confirm(
        `删除「${attachment.original_name}」？删除后仍占用存储空间，` +
          '可在“含已删除”中找到并彻底删除，以释放空间。',
        {
          title: '删除附件',
          okText: '删除',
          cancelText: '取消',
          severity: 'warning',
        },
      );
      if (!confirmed) {
        return;
      }
      setBusyId(attachment.id);
      try {
        await deleteAttachment(attachment.id);
        show('附件已删除；如需释放空间，请在“含已删除”中彻底删除。', { severity: 'success' });
        refreshFromFirstPage();
      } catch (caught) {
        handle(toApiError(caught));
      } finally {
        setBusyId(null);
      }
    },
    [dialogs, handle, refreshFromFirstPage, show],
  );

  const handlePurge = React.useCallback(
    async (attachment: Attachment) => {
      const confirmed = await dialogs.confirm(
        `彻底删除「${attachment.original_name}」？此操作不可恢复，删除后将释放该附件占用的空间。`,
        {
          title: '彻底删除附件',
          okText: '彻底删除',
          cancelText: '取消',
          severity: 'error',
        },
      );
      if (!confirmed) {
        return;
      }
      setBusyId(attachment.id);
      try {
        await purgeAttachment(attachment.id);
        show('附件已彻底删除。', { severity: 'success' });
        refreshFromFirstPage();
      } catch (caught) {
        handle(toApiError(caught));
      } finally {
        setBusyId(null);
      }
    },
    [dialogs, handle, refreshFromFirstPage, show],
  );

  const handleDownload = React.useCallback(
    async (attachment: Attachment) => {
      setDownloadingId(attachment.id);
      try {
        const { blob, filename } = await downloadAttachment(attachment.id);
        const url = URL.createObjectURL(blob);
        const anchor = document.createElement('a');
        anchor.href = url;
        anchor.download = filename ?? attachment.original_name;
        document.body.appendChild(anchor);
        anchor.click();
        anchor.remove();
        // 给浏览器一个读取 blob 的时机再回收；立即 revoke 在部分浏览器会得到空文件
        window.setTimeout(() => URL.revokeObjectURL(url), 1000);
      } catch (caught) {
        handle(toApiError(caught));
      } finally {
        setDownloadingId(null);
      }
    },
    [handle],
  );

  const canPurge = React.useCallback(
    (attachment: Attachment) =>
      Boolean(attachment.deleted_at) &&
      (user?.system_role === 'admin' ||
        attachment.uploader_id === user?.id ||
        canPurgeAll),
    [canPurgeAll, user?.id, user?.system_role],
  );

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <Stack spacing={1.5}>
      {preview && <React.Suspense fallback={null}>
        <AttachmentPreview attachmentId={preview.id} fileName={preview.original_name} knownSize={preview.size_bytes} onClose={() => setPreview(null)} />
      </React.Suspense>}
      <Stack
        direction="row"
        sx={{ alignItems: 'center', justifyContent: 'space-between', gap: 1, flexWrap: 'wrap' }}
      >
        <Box>
          <Typography
            variant={dense ? 'subtitle2' : 'h6'}
            component={dense ? 'span' : 'h2'}
            color={dense ? 'text.secondary' : undefined}
          >
            附件
            <Typography component="span" variant="body2" color="text.secondary" sx={{ ml: 1 }}>
              {total > 0 ? `共 ${total} 个` : '暂无'}
            </Typography>
          </Typography>
          {!dense ? (
            <Typography variant="caption" color="text.secondary">
              每次上传一个文件。删除后仍占用空间，彻底删除后才会释放。
            </Typography>
          ) : null}
        </Box>

        <Stack direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap' }}>
          <FormControlLabel
            sx={{ mr: 0 }}
            control={
              <Switch
                size="small"
                checked={includeDeleted}
                onChange={(event) => {
                  setIncludeDeleted(event.target.checked);
                  setPage(0);
                }}
              />
            }
            label={<Typography variant="body2">含已删除</Typography>}
          />
          <Tooltip title={canWrite ? '上传一个附件' : '当前状态下不可上传（容器已归档，或你没有写权限）'}>
            <span>
              <Button
                component="label"
                variant="outlined"
                startIcon={uploading ? <CircularProgress size={16} /> : <UploadFileIcon />}
                disabled={!canWrite || uploading}
              >
                {uploading ? '上传中…' : '上传附件'}
                <input
                  type="file"
                  hidden
                  disabled={!canWrite || uploading}
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    // 清空 value，保证同一个文件失败后还能再次选择
                    event.target.value = '';
                    if (file) {
                      void handleUpload(file);
                    }
                  }}
                />
              </Button>
            </span>
          </Tooltip>
          <Tooltip title="刷新附件">
            <span>
              <IconButton
                size="small"
                aria-label="刷新附件"
                onClick={reload}
                disabled={loading}
              >
                <RefreshIcon fontSize="small" />
              </IconButton>
            </span>
          </Tooltip>
        </Stack>
      </Stack>

      {loading ? <LinearProgress /> : null}

      {error ? (
        <Alert
          severity={error.isNotFound ? 'info' : 'error'}
          action={
            <Button color="inherit" onClick={reload}>
              重试
            </Button>
          }
        >
          {describeApiError(error)}
        </Alert>
      ) : null}

      {uploadError ? (
        <Alert severity="error" onClose={() => setUploadError(null)}>
          {uploadError.fields.file ?? describeApiError(uploadError)}
        </Alert>
      ) : null}

      {!loading && !error && items.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          暂无附件{includeDeleted ? '（含已删除）' : ''}。
        </Typography>
      ) : null}

      <Stack divider={<Divider flexItem />} spacing={1}>
        {items.map((attachment) => (
          <Stack
            key={attachment.id}
            direction="row"
            spacing={1.5}
            sx={{ alignItems: 'center', justifyContent: 'space-between' }}
          >
            <Stack direction="row" spacing={1} sx={{ alignItems: 'center', minWidth: 0, flex: 1 }}>
              <InsertDriveFileIcon color="action" fontSize="small" />
              <Box sx={{ minWidth: 0 }}>
                <Stack direction="row" spacing={1} sx={{ alignItems: 'center', minWidth: 0 }}>
                  <Link component="button" type="button" onClick={() => setPreview(attachment)}
                    aria-label={`预览 ${attachment.original_name}`} sx={{ minWidth: 0, textAlign: 'left' }}>
                    {truncatedText(attachment.original_name, {
                      threshold: 28, keep: 24, ellipsis: '…',
                      typographyProps: { component: 'span', sx: { fontWeight: 500 } },
                    })}
                  </Link>
                  {attachment.deleted_at ? (
                    <StatusTag label="已删除" tone="default" variant="outlined" />
                  ) : null}
                </Stack>
                <Typography
                  variant="caption"
                  color="text.secondary"
                  component="div"
                  sx={{ wordBreak: 'break-all' }}
                >
                  {formatBytes(attachment.size_bytes)}
                  {attachment.extension ? ` · ${attachment.extension}` : ''}
                  {' · '}
                  {formatForDisplay(attachment.created_at, userTimeZone)}
                  {' · 上传者 '}
                  <Tooltip title={attachment.uploader_id}>
                    <span>{attachment.uploader_id.slice(-8)}</span>
                  </Tooltip>
                </Typography>
              </Box>
            </Stack>

            <Stack direction="row" spacing={0.5} sx={{ alignItems: 'center' }}>
              <Tooltip
                title={
                  attachment.deleted_at
                    ? '下载（已删除附件仅上传者/持有者/管理员可见）'
                    : '下载'
                }
              >
                <span>
                  <IconButton
                    size="small"
                    aria-label={`下载 ${attachment.original_name}`}
                    onClick={() => void handleDownload(attachment)}
                    disabled={downloadingId === attachment.id}
                  >
                    {downloadingId === attachment.id ? (
                      <CircularProgress size={16} />
                    ) : (
                      <DownloadIcon fontSize="small" />
                    )}
                  </IconButton>
                </span>
              </Tooltip>

              {!attachment.deleted_at && canWrite ? (
                <Tooltip title="删除附件（仍占用空间）">
                  <span>
                    <IconButton
                      size="small"
                      aria-label={`删除 ${attachment.original_name}`}
                      onClick={() => void handleDelete(attachment)}
                      disabled={busyId === attachment.id}
                    >
                      <DeleteIcon fontSize="small" />
                    </IconButton>
                  </span>
                </Tooltip>
              ) : null}

              {attachment.deleted_at && canPurge(attachment) ? (
                <Tooltip title="彻底删除（不可恢复，释放空间）">
                  <span>
                    <IconButton
                      size="small"
                      color="error"
                      aria-label={`彻底删除 ${attachment.original_name}`}
                      onClick={() => void handlePurge(attachment)}
                      disabled={busyId === attachment.id}
                    >
                      <DeleteForeverIcon fontSize="small" />
                    </IconButton>
                  </span>
                </Tooltip>
              ) : null}
            </Stack>
          </Stack>
        ))}
      </Stack>

      {total > PAGE_SIZE ? (
        <Stack direction="row" spacing={1} sx={{ alignItems: 'center', justifyContent: 'flex-end' }}>
          <Typography variant="caption" color="text.secondary">
            第 {page + 1} / {totalPages} 页
          </Typography>
          <Button
            disabled={page === 0 || loading}
            onClick={() => setPage((current) => Math.max(0, current - 1))}
          >
            上一页
          </Button>
          <Button
            disabled={page + 1 >= totalPages || loading}
            onClick={() => setPage((current) => Math.min(totalPages - 1, current + 1))}
          >
            下一页
          </Button>
        </Stack>
      ) : null}
    </Stack>
  );
}
