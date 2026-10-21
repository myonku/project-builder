/**
 * 用户头像（P01 §4.6）。
 *
 * 规则：
 * - 有 `avatar_updated_at` 时按 `id + updated_at` 拼 URL，版本变化自动破缓存；
 * - 没有头像 / 图片加载失败 / 没有显示名时回退首字 + 稳定底色；
 * - 颜色按 **user_id** 哈希（不是显示名），改名不会变色；
 * - 所有列表、成员面板、计划表、甘特图与顶栏共用本组件。
 */

import * as React from 'react';
import Avatar from '@mui/material/Avatar';
import type { SxProps, Theme } from '@mui/material/styles';
import { avatarUrl } from '@/api/users';
import defaultAvatar from '@/assets/brand/default-avatar.png';

export interface UserAvatarUser {
  /** 公开用户对象使用 `id`；计划视图成员对象使用 `user_id` */
  id?: string;
  user_id?: string;
  display_name?: string | null;
  avatar_updated_at?: string | null;
}

export interface UserAvatarProps {
  user: UserAvatarUser | null | undefined;
  size?: number;
  sx?: SxProps<Theme>;
  /** 装饰性重复头像可传空字符串（如表格里已有姓名） */
  alt?: string;
}

/** 稳定调色板：同一 user_id 永远得到同一种底色。 */
const PALETTE = ['#1565c0', '#2e7d32', '#ad1457', '#6a1b9a', '#ef6c00', '#00838f', '#4527a0'];

function colorFor(id: string): string {
  let hash = 0;
  for (let index = 0; index < id.length; index += 1) {
    hash = (hash * 31 + id.charCodeAt(index)) >>> 0;
  }
  return PALETTE[hash % PALETTE.length];
}

function initialOf(user: UserAvatarUser | null | undefined): string {
  const name = user?.display_name?.trim();
  return name ? name.charAt(0).toUpperCase() : '?';
}

function idOf(user: UserAvatarUser | null | undefined): string {
  return user?.id ?? user?.user_id ?? '';
}

export default function UserAvatar({ user, size = 24, sx, alt }: UserAvatarProps) {
  const [failed, setFailed] = React.useState(false);
  const version = user?.avatar_updated_at ?? null;
  const userId = idOf(user);

  React.useEffect(() => {
    // 换头像后重新尝试加载，避免一次失败永久回退。
    setFailed(false);
  }, [version, userId]);

  const src = userId && version ? avatarUrl(userId, version) : user && !user.display_name?.trim() ? defaultAvatar : undefined;
  const label = alt ?? user?.display_name ?? '';

  return (
    <Avatar
      alt={label}
      src={failed ? undefined : src}
      slotProps={{
        img: {
          loading: 'lazy',
          onError: () => setFailed(true),
        },
      }}
      sx={{
        width: size,
        height: size,
        fontSize: Math.max(10, Math.round(size * 0.42)),
        bgcolor: userId ? colorFor(userId) : 'grey.500',
        flexShrink: 0,
        ...sx,
      }}
    >
      {initialOf(user)}
    </Avatar>
  );
}
