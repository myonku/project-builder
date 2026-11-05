import { Navigate, useLocation } from 'react-router';

/** Preserve bookmarks from the former tab page without adding a history entry. */
export default function StatisticsView() {
  const { search, hash } = useLocation();
  const params = new URLSearchParams(search);
  const page = params.get('page') === 'report' ? 'reports' : 'overview';
  if (!params.has('scope') && params.get('tab') === 'members') params.set('scope', 'member');
  params.delete('page');
  params.delete('tab');
  const query = params.toString();
  return <Navigate replace to={`/statistics/${page}${query ? `?${query}` : ''}${hash}`} />;
}
