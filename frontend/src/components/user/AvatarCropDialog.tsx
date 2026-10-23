import * as React from 'react';
import Cropper from 'react-easy-crop';
import type { Area } from 'react-easy-crop';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import CircularProgress from '@mui/material/CircularProgress';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogTitle from '@mui/material/DialogTitle';
import Slider from '@mui/material/Slider';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import { describeApiError } from '@/hooks/useApiError';

interface Props {
  file: File;
  busy: boolean;
  error: unknown;
  onCancel: () => void;
  onSave: (file: File) => Promise<void>;
}

function cropCanvas(image: HTMLImageElement, area: Area, rotation: number): HTMLCanvasElement {
  const canvas = document.createElement('canvas');
  canvas.width = 512; canvas.height = 512;
  const context = canvas.getContext('2d');
  if (!context) throw new Error('无法处理这张图片，请重新选择。');
  context.imageSmoothingQuality = 'high';
  // Crop coordinates refer to the bounding box of the rotated source image.
  // Draw directly into the output to avoid allocating a large intermediate bitmap.
  const radians = rotation * Math.PI / 180;
  const width = image.naturalWidth, height = image.naturalHeight;
  const boundsWidth = Math.abs(Math.cos(radians) * width) + Math.abs(Math.sin(radians) * height);
  const boundsHeight = Math.abs(Math.sin(radians) * width) + Math.abs(Math.cos(radians) * height);
  context.scale(512 / area.width, 512 / area.height);
  context.translate(boundsWidth / 2 - area.x, boundsHeight / 2 - area.y);
  context.rotate(radians);
  context.drawImage(image, -width / 2, -height / 2);
  return canvas;
}

export default function AvatarCropDialog({ file, busy, error, onCancel, onSave }: Props) {
  const titleId = React.useId();
  const [loaded, setLoaded] = React.useState<{ image: HTMLImageElement; url: string } | null>(null);
  const [crop, setCrop] = React.useState({ x: 0, y: 0 });
  const [zoom, setZoom] = React.useState(1);
  const [rotation, setRotation] = React.useState(0);
  const [area, setArea] = React.useState<Area | null>(null);
  const [localError, setLocalError] = React.useState('');
  const [encoding, setEncoding] = React.useState(false);
  const [interacting, setInteracting] = React.useState(false);
  const working = busy || encoding;

  React.useEffect(() => {
    const url = URL.createObjectURL(file);
    const image = new Image();
    let cancelled = false;
    image.onload = () => {
      if (cancelled) return;
      if (image.naturalWidth * image.naturalHeight > 24_000_000 || Math.max(image.naturalWidth, image.naturalHeight) > 8192) {
        setLocalError('图片尺寸过大，请选择 2400 万像素以内、边长不超过 8192 像素的图片。');
      } else setLoaded({ image, url });
    };
    image.onerror = () => { if (!cancelled) setLocalError('图片无法读取，请重新选择 PNG 或 JPEG 图片。'); };
    image.src = url;
    return () => { cancelled = true; image.onload = null; image.onerror = null; image.src = ''; URL.revokeObjectURL(url); };
  }, [file]);

  async function save() {
    if (!loaded || !area || working || interacting) return;
    setEncoding(true); setLocalError('');
    try {
      const canvas = cropCanvas(loaded.image, area, rotation);
      const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob(value => value ? resolve(value) : reject(new Error('图片保存失败，请重试。')), 'image/png'));
      if (blob.size > 2 * 1024 * 1024) throw new Error('裁剪结果超过头像大小限制，请重新选择图片。');
      await onSave(new File([blob], 'avatar.png', { type: 'image/png' }));
    } catch (cause) { setLocalError(cause instanceof Error ? cause.message : '图片保存失败，请重试。'); }
    finally { setEncoding(false); }
  }

  return <Dialog open fullWidth maxWidth="sm" aria-labelledby={titleId}
    onClose={(_event, reason) => { if (!working && reason !== 'backdropClick') onCancel(); }}>
    <DialogTitle id={titleId}>设置头像</DialogTitle>
    <DialogContent>
      <Stack spacing={2}>
        <Typography variant="body2" color="text.secondary">拖动图片调整位置，使用缩放和旋转滑块，让头像在圆框中呈现合适的效果。</Typography>
        {localError && <Alert severity="error">{localError}</Alert>}
        {!!error && <Alert severity="error">{describeApiError(error)}</Alert>}
        <Box sx={{ position: 'relative', height: { xs: 240, sm: 280 }, bgcolor: 'grey.900', borderRadius: 1, overflow: 'hidden', pointerEvents: working ? 'none' : 'auto' }}>
          {loaded ? <Cropper image={loaded.url} crop={crop} zoom={zoom} rotation={rotation} aspect={1} minZoom={1} maxZoom={4}
            cropShape="round" showGrid={false} onCropChange={setCrop} onZoomChange={setZoom} onRotationChange={setRotation}
            onCropComplete={(_percent, pixels) => setArea(pixels)}
            onInteractionStart={() => setInteracting(true)} onInteractionEnd={() => setInteracting(false)}
            cropperProps={{ 'aria-label': '头像裁剪区域' }} />
            : !localError && <CircularProgress size={28} sx={{ position: 'absolute', top: '50%', left: '50%' }} aria-label="正在读取图片" />}
        </Box>
        <Box>
          <Typography id="avatar-zoom-label" variant="body2">缩放</Typography>
          <Slider aria-labelledby="avatar-zoom-label" value={zoom} min={1} max={4} step={0.01} valueLabelDisplay="auto"
            valueLabelFormat={value => `${Math.round(value * 100)}%`} onChange={(_event, value) => setZoom(value as number)} disabled={!loaded || working} />
        </Box>
        <Box>
          <Typography id="avatar-rotation-label" variant="body2">旋转</Typography>
          <Slider aria-labelledby="avatar-rotation-label" value={rotation} min={-180} max={180} step={1} valueLabelDisplay="auto"
            valueLabelFormat={value => `${value}°`} onChange={(_event, value) => setRotation(value as number)} disabled={!loaded || working} />
        </Box>
      </Stack>
    </DialogContent>
    <DialogActions sx={{ px: 3, pb: 2, flexWrap: 'wrap', gap: 1 }}>
      <Button onClick={() => { setCrop({ x: 0, y: 0 }); setZoom(1); setRotation(0); }} disabled={working || !loaded}>重置裁剪</Button>
      <Box sx={{ flex: 1 }} />
      <Button onClick={onCancel} disabled={working}>取消</Button>
      <Button variant="contained" onClick={() => void save()} disabled={!loaded || !area || working || interacting}>
        {working ? '保存中…' : '保存头像'}
      </Button>
    </DialogActions>
  </Dialog>;
}
