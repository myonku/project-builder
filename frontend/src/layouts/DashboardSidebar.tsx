// Source: MUI material-ui v9.5.0
// Path: docs/data/material/getting-started/templates/crud-dashboard/components/DashboardSidebar.tsx
// Modified: 相对导入改为同层；导航项替换为本项目路由

import * as React from 'react';
import { useTheme, type Theme } from '@mui/material/styles';
import useMediaQuery from '@mui/material/useMediaQuery';
import Box from '@mui/material/Box';
import Drawer from '@mui/material/Drawer';
import List from '@mui/material/List';
import Toolbar from '@mui/material/Toolbar';
import type {} from '@mui/material/themeCssVarsAugmentation';
import DashboardIcon from '@mui/icons-material/Dashboard';
import FolderIcon from '@mui/icons-material/Folder';
import EventNoteIcon from '@mui/icons-material/EventNote';
import TimelineIcon from '@mui/icons-material/Timeline';
import AssignmentIndIcon from '@mui/icons-material/AssignmentInd';
import GroupsIcon from '@mui/icons-material/Groups';
import NotificationsIcon from '@mui/icons-material/Notifications';
import HistoryIcon from '@mui/icons-material/History';
import ManageAccountsIcon from '@mui/icons-material/ManageAccounts';
import HowToRegIcon from '@mui/icons-material/HowToReg';
import CorporateFareIcon from '@mui/icons-material/CorporateFare';
import { useLocation } from 'react-router';
import { useAuthStore } from '@/stores/auth';
import DashboardSidebarContext from './DashboardSidebarContext';
import { DRAWER_WIDTH, MINI_DRAWER_WIDTH } from './constants';
import DashboardSidebarPageItem from './DashboardSidebarPageItem';
import DashboardSidebarHeaderItem from './DashboardSidebarHeaderItem';
import DashboardSidebarDividerItem from './DashboardSidebarDividerItem';
import getDrawerSxTransitionMixin from './mixins';

export interface DashboardSidebarProps {
  expanded?: boolean;
  setExpanded: (expanded: boolean) => void;
  disableCollapsibleSidebar?: boolean;
  container?: Element;
}

export default function DashboardSidebar({
  expanded = true,
  setExpanded,
  disableCollapsibleSidebar = false,
  container,
}: DashboardSidebarProps) {
  const theme = useTheme();

  const { pathname } = useLocation();

  // 操作记录与管理入口只对系统管理员显示；真正的守卫在 router/guards.tsx 的 RequireAdmin
  const isAdmin = useAuthStore((state) => state.user?.system_role === 'admin');

  /**
   * 三级菜单里"计划"这一组的展开状态。
   *
   * **默认全部展开**（用户要求：进来就能看到所有入口，不用先点一下）：
   * 初值直接给 `['plan']`，并且当路由落在计划页时也保持展开——
   * 这样即使手点收起，切到 `/plans/*` 仍然能看到子项高亮。
   */
  const isPlanRoute = pathname === '/plans/personal' || pathname === '/plans/group';
  const [expandedItemIds, setExpandedItemIds] = React.useState<string[]>(['plan']);

  React.useEffect(() => {
    if (isPlanRoute) {
      setExpandedItemIds((previous) =>
        previous.includes('plan') ? previous : [...previous, 'plan'],
      );
    }
  }, [isPlanRoute]);

  const isOverSmViewport = useMediaQuery(theme.breakpoints.up('sm'));
  const isOverMdViewport = useMediaQuery(theme.breakpoints.up('md'));
  const prefersReducedMotion = useMediaQuery('(prefers-reduced-motion: reduce)');
  const shouldReduceDrawerMotion =
    theme.motion.reducedMotion === 'always' ||
    (theme.motion.reducedMotion === 'system' && prefersReducedMotion);
  const drawerEnteringDuration = shouldReduceDrawerMotion
    ? 0
    : theme.transitions.duration.enteringScreen;
  const drawerLeavingDuration = shouldReduceDrawerMotion
    ? 0
    : theme.transitions.duration.leavingScreen;

  const [isFullyExpanded, setIsFullyExpanded] = React.useState(expanded);
  const [isFullyCollapsed, setIsFullyCollapsed] = React.useState(!expanded);

  React.useEffect(() => {
    if (expanded) {
      if (drawerEnteringDuration === 0) {
        setIsFullyExpanded(true);
        return undefined;
      }

      const drawerWidthTransitionTimeout = setTimeout(() => {
        setIsFullyExpanded(true);
      }, drawerEnteringDuration);

      return () => clearTimeout(drawerWidthTransitionTimeout);
    }

    setIsFullyExpanded(false);

    return undefined;
  }, [drawerEnteringDuration, expanded]);

  React.useEffect(() => {
    if (!expanded) {
      if (drawerLeavingDuration === 0) {
        setIsFullyCollapsed(true);
        return undefined;
      }

      const drawerWidthTransitionTimeout = setTimeout(() => {
        setIsFullyCollapsed(true);
      }, drawerLeavingDuration);

      return () => clearTimeout(drawerWidthTransitionTimeout);
    }

    setIsFullyCollapsed(false);

    return undefined;
  }, [drawerLeavingDuration, expanded]);

  const mini = !disableCollapsibleSidebar && !expanded;

  const handleSetSidebarExpanded = React.useCallback(
    (newExpanded: boolean) => () => {
      setExpanded(newExpanded);
    },
    [setExpanded],
  );

  const handlePageItemClick = React.useCallback(
    (itemId: string, hasNestedNavigation: boolean) => {
      if (hasNestedNavigation && !mini) {
        setExpandedItemIds((previousValue) =>
          previousValue.includes(itemId)
            ? previousValue.filter(
                (previousValueItemId) => previousValueItemId !== itemId,
              )
            : [...previousValue, itemId],
        );
      } else if (!isOverSmViewport && !hasNestedNavigation) {
        setExpanded(false);
      }
    },
    [mini, setExpanded, isOverSmViewport],
  );

  const hasDrawerTransitions =
    isOverSmViewport && (!disableCollapsibleSidebar || isOverMdViewport);

  const getDrawerContent = React.useCallback(
    (viewport: 'phone' | 'tablet' | 'desktop') => (
      <React.Fragment>
        <Toolbar />
        <Box
          component="nav"
          aria-label={`${viewport.charAt(0).toUpperCase()}${viewport.slice(1)}`}
          sx={[
            {
              height: '100%',
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'space-between',
              overflow: 'auto',
              scrollbarGutter: mini ? 'stable' : 'auto',
              overflowX: 'hidden',
              pt: !mini ? 0 : 2,
            },
            hasDrawerTransitions
              ? getDrawerSxTransitionMixin(isFullyExpanded, 'padding')
              : null,
          ]}
        >
          <List
            dense
            sx={{
              padding: mini ? 0 : 0.5,
              mb: 4,
              width: mini ? MINI_DRAWER_WIDTH : 'auto',
            }}
          >
            {/*
              导航层级（用户要求）：
              - 一级 = 分组标题（工作台 / 系统管理）
              - 二级 = 概览、项目、计划、视图、邮件提醒、操作记录 / 用户管理、工作组管理
              - 三级 = 计划 下的 个人计划、组内计划（默认展开，可点击"计划"收起）
            */}
            <DashboardSidebarHeaderItem>首页</DashboardSidebarHeaderItem>
            <DashboardSidebarPageItem
              id="dashboard"
              title="概览"
              icon={<DashboardIcon />}
              href="/"
              selected={pathname === '/'}
            />
            <DashboardSidebarHeaderItem>工作台</DashboardSidebarHeaderItem>
            <DashboardSidebarPageItem
              id="projects"
              title="项目"
              icon={<FolderIcon />}
              href="/projects"
              selected={pathname === '/projects' || pathname.startsWith('/projects/')}
            />
            <DashboardSidebarPageItem
              id="plan"
              title="计划"
              icon={<EventNoteIcon />}
              // 有子项时该行只做展开/收起，不参与跳转；折叠态（mini）下悬停弹出子项
              href="/plans"
              selected={isPlanRoute}
              defaultExpanded
              expanded={expandedItemIds.includes('plan')}
              nestedNavigation={
                <List
                  dense
                  sx={{
                    padding: 0,
                    my: 0.5,
                    pl: mini ? 0 : 2,
                    minWidth: 220,
                  }}
                >
                  <DashboardSidebarPageItem
                    id="personal-plan"
                    title="个人计划"
                    icon={<AssignmentIndIcon />}
                    href="/plans/personal"
                    selected={pathname === '/plans/personal'}
                  />
                  <DashboardSidebarPageItem
                    id="group-plan"
                    title="组内计划"
                    icon={<GroupsIcon />}
                    href="/plans/group"
                    selected={pathname === '/plans/group'}
                  />
                </List>
              }
            />
            <DashboardSidebarPageItem
              id="gantt"
              title="视图"
              icon={<TimelineIcon />}
              href="/gantt"
              selected={pathname === '/gantt'}
            />
            <DashboardSidebarPageItem
              id="reminders"
              title="邮件提醒"
              icon={<NotificationsIcon />}
              href="/reminders"
              selected={pathname === '/reminders'}
            />
            {isAdmin ? (
              <React.Fragment>
                {/* 审计仅系统管理员可见（《A09》）：非管理员连入口都不显示 */}
                <DashboardSidebarPageItem
                  id="audit"
                  title="操作记录"
                  icon={<HistoryIcon />}
                  href="/audit"
                  selected={pathname === '/audit'}
                />
                <DashboardSidebarDividerItem />
                <DashboardSidebarHeaderItem>系统管理</DashboardSidebarHeaderItem>
                <DashboardSidebarPageItem
                  id="admin-users"
                  title="用户管理"
                  icon={<ManageAccountsIcon />}
                  href="/admin/users"
                  selected={pathname === '/admin/users'}
                />
                <DashboardSidebarPageItem
                  id="admin-registration-requests"
                  title="注册申请"
                  icon={<HowToRegIcon />}
                  href="/admin/registration-requests"
                  selected={pathname === '/admin/registration-requests'}
                />
                <DashboardSidebarPageItem
                  id="admin-groups"
                  title="工作组管理"
                  icon={<CorporateFareIcon />}
                  href="/admin/groups"
                  selected={pathname === '/admin/groups'}
                />
              </React.Fragment>
            ) : null}
          </List>
        </Box>
      </React.Fragment>
    ),
    [mini, hasDrawerTransitions, isFullyExpanded, expandedItemIds, pathname, isAdmin],
  );

  const getDrawerSharedSx = React.useCallback(
    (isTemporary: boolean) => (drawerTheme: Theme) => {
      const drawerWidth = mini ? MINI_DRAWER_WIDTH : DRAWER_WIDTH;
      const widthTransitionStyles = getDrawerSxTransitionMixin(
        expanded,
        'width',
      )(drawerTheme);

      return {
        displayPrint: 'none',
        width: drawerWidth,
        flexShrink: 0,
        ...widthTransitionStyles,
        overflowX: 'hidden',
        ...(isTemporary ? { position: 'absolute' } : {}),
        [`& .MuiDrawer-paper`]: {
          position: 'absolute',
          width: drawerWidth,
          boxSizing: 'border-box',
          backgroundImage: 'none',
          ...widthTransitionStyles,
          overflowX: 'hidden',
        },
      };
    },
    [expanded, mini],
  );

  const sidebarContextValue = React.useMemo(() => {
    return {
      onPageItemClick: handlePageItemClick,
      mini,
      fullyExpanded: isFullyExpanded,
      fullyCollapsed: isFullyCollapsed,
      hasDrawerTransitions,
    };
  }, [
    handlePageItemClick,
    mini,
    isFullyExpanded,
    isFullyCollapsed,
    hasDrawerTransitions,
  ]);

  return (
    <DashboardSidebarContext.Provider value={sidebarContextValue}>
      <Drawer
        container={container}
        variant="temporary"
        open={expanded}
        onClose={handleSetSidebarExpanded(false)}
        ModalProps={{
          keepMounted: true, // Better open performance on mobile.
        }}
        sx={[
          {
            display: {
              xs: 'block',
              sm: disableCollapsibleSidebar ? 'block' : 'none',
              md: 'none',
            },
          },
          getDrawerSharedSx(true),
        ]}
      >
        {getDrawerContent('phone')}
      </Drawer>
      <Drawer
        variant="permanent"
        sx={[
          {
            display: {
              xs: 'none',
              sm: disableCollapsibleSidebar ? 'none' : 'block',
              md: 'none',
            },
          },
          getDrawerSharedSx(false),
        ]}
      >
        {getDrawerContent('tablet')}
      </Drawer>
      <Drawer
        variant="permanent"
        sx={[{ display: { xs: 'none', md: 'block' } }, getDrawerSharedSx(false)]}
      >
        {getDrawerContent('desktop')}
      </Drawer>
    </DashboardSidebarContext.Provider>
  );
}
