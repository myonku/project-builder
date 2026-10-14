/**
 * UI 窄状态（F01 §4）。
 *
 * 这里**只有当前工作组 id**：它是跨页面共享的（项目列表、组内计划、成员管理都要用），
 * 而侧边栏展开态被 `DashboardLayout` 自己持有——它没有跨页面消费者，
 * 放进全局 store 只会多一层同步（这一点与 F01 §3 的原始描述不同，已在文档中记录）。
 */

import { create } from 'zustand';

export interface UiState {
  /** 当前选中的工作组；null 表示"未选择"（页面需要自己兜底或提示选择） */
  currentGroupId: string | null;
  setCurrentGroupId: (groupId: string | null) => void;
}

export const useUiStore = create<UiState>()((set) => ({
  currentGroupId: null,
  setCurrentGroupId: (groupId) => set({ currentGroupId: groupId }),
}));
