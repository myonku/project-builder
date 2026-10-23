import { useNavigate, useParams } from 'react-router';
import AttachmentPreview from '@/components/attachment/AttachmentPreview';
export default function AttachmentPreviewView() {
  const { attachmentId = '' } = useParams();
  const navigate = useNavigate();
  function close() {
    window.close();
    // Browsers only close tabs opened by a script/link; direct URLs return to projects.
    if (!window.closed) navigate('/projects', { replace: true });
  }
  return <AttachmentPreview attachmentId={attachmentId} standalone onClose={close} />;
}
