import { Notice } from "@labstack/ui";
import { ColorSchemeToggle } from "./color-scheme-toggle";
import { ActionIcon, AppShell, Avatar, Box, Burger, Button, Drawer, Group, Menu } from "@mantine/core";
import { useDisclosure, useMediaQuery } from "@mantine/hooks";
import { MagnifyingGlass, SidebarSimple, PlugsConnected, SignOut } from "@phosphor-icons/react";
import { useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import { lazy, Suspense, useEffect, useRef, useState, type ReactNode } from "react";
import { createDashboardPrompt, useFanoutApp } from "./app-context";
import { logout, useViewer } from "./auth";
import { BrandLockup } from "./brand";
import { useOpener } from "./opener";
import { HelpMenu } from "./help-menu";

import Rail, { type RailHandle, type RailProps } from "./rail";

// The editor and its CodeMirror load only when a dashboard is being created.
const NewDashboard = lazy(() => import("./dashboards/new-dashboard").then(module => ({ default: module.NewDashboard })));

export default function Shell({ children }: { children: ReactNode }) {
  const { agentAvailable, threadID, threadMissing, newThread, selectThread, openChat } = useFanoutApp();
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const { dashboardId } = useParams({ strict: false }) as { dashboardId?: string };
  const isChat = pathname === "/chat" || pathname.startsWith("/chat/");
  const [drawerOpened, drawer] = useDisclosure(false);
  // Why the drawer opened decides whether search takes focus: a burger tap is a
  // request to read the lists, and focusing search there raises the phone
  // keyboard over them. Only ⌘K is asking to search.
  const [collapsed, setCollapsed] = useState(false);
  const [keyboardOpen, setKeyboardOpen] = useState(false);
  const [signOutError, setSignOutError] = useState("");
  const [creating, setCreating] = useState(false);
  const railRef = useRef<RailHandle>(null);
  const burger = useRef<HTMLButtonElement>(null);
  const opener = useOpener();
  // The navbar stays mounted below `md`, only collapsed, so its ref is never
  // null and cannot say which rail the viewer can see. The breakpoint can.
  const mobile = useMediaQuery("(max-width: 62em)");
  const phone = useMediaQuery("(max-width: 48em)");
  const mobileRef = useRef(mobile);
  const openSearch = () => { if (mobile) { setKeyboardOpen(true); drawer.open(); } else { setCollapsed(false); requestAnimationFrame(() => railRef.current?.focusSearch()); } };
  mobileRef.current = mobile;

  // A navigation from inside the drawer should close it.
  useEffect(() => { drawer.close(); }, [pathname]);

  useEffect(() => {
    const shortcuts = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        if (mobileRef.current) { setKeyboardOpen(true); drawer.open(); } else { setCollapsed(false); requestAnimationFrame(() => railRef.current?.focusSearch()); }
      }
    };
    window.addEventListener("keydown", shortcuts);
    return () => window.removeEventListener("keydown", shortcuts);
  }, []);

  const railProps: Omit<RailProps, "ref"> = {
    agentAvailable,
    activeThreadID: isChat && !threadMissing && pathname !== "/chat" ? threadID : undefined,
    activeDashboardID: dashboardId,
    activePath: pathname,
    onNewChat: newThread,
    onSelectThread: selectThread,
    onDeletedThread: (deletedID) => { if (deletedID === threadID) newThread(); },
    onSelectDashboard: (id) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId: id }, search: {} }),
    onCreateDashboard: () => openChat(createDashboardPrompt),
    // The editor opens over the page, not over the mobile drawer, and returns focus to the burger that opened the drawer.
    onNewDashboard: () => { opener.remember(mobileRef.current ? burger.current : document.activeElement); drawer.close(); setCreating(true); },
    onNavigate: (path) => void navigate({ to: path }),
    onInvestigateService: (service) => openChat(`Investigate the ${service} service. Explain its errors and latency.`),
  };

  const closeNew = () => { setCreating(false); opener.restore(); };

  return <AppShell header={{ height: 56 }} navbar={{ width: collapsed ? 56 : 248, breakpoint: "md", collapsed: { mobile: true } }} padding={0}>
    <a className="skip-link" href="#main">Skip to content</a>
    <AppShell.Header>
      <Group h="100%" px={{ base: "sm", sm: "md" }} justify="space-between" wrap="nowrap">
        <Group gap="sm" wrap="nowrap">
          <Burger ref={burger} hiddenFrom="md" opened={drawerOpened} aria-expanded={drawerOpened} onClick={() => { setKeyboardOpen(false); drawer.toggle(); }} size="sm" aria-label={drawerOpened ? "Close navigation" : "Open navigation"} />
          <ActionIcon visibleFrom="md" variant="subtle" color="gray" aria-label={collapsed ? "Expand navigation" : "Collapse navigation"} aria-expanded={!collapsed} onClick={() => setCollapsed(value => !value)}><SidebarSimple size={18} /></ActionIcon>
          <BrandLockup size="small" />
        </Group>
        {phone
          ? <ActionIcon variant="default" size="lg" aria-label="Search" onClick={openSearch}><MagnifyingGlass size={16} /></ActionIcon>
          : <Button variant="default" color="gray" leftSection={<MagnifyingGlass size={16} />} onClick={openSearch}>Search · ⌘K</Button>}
        <Box id="shell-time-range" visibleFrom="sm" />
        <Group gap="xs" wrap="nowrap">
          {/* Help sits in the chrome, not in the account menu: it answers a
              question someone has while working, and the account menu is for
              acting on your own account. Keyboard shortcuts live here, as in
              most consoles, rather than as a button in each page's toolbar. */}
          <HelpMenu />
          <ColorSchemeToggle />
          <AccountMenu onError={setSignOutError} />
        </Group>
      </Group>
    </AppShell.Header>
    <AppShell.Navbar p={collapsed ? 4 : "sm"}><Rail ref={railRef} {...railProps} collapsed={collapsed} /></AppShell.Navbar>
    <Drawer opened={drawerOpened} onClose={drawer.close} returnFocus={!creating} hiddenFrom="md" size={288} padding="sm" title={<BrandLockup size="small" />} overlayProps={{ backgroundOpacity: 0.24, blur: 1 }}>
      <Rail {...railProps} autoFocusSearch={drawerOpened && keyboardOpen} />
    </Drawer>
    <AppShell.Main id="main" tabIndex={-1}>
      {signOutError && <Notice tone="danger" onDismiss={() => setSignOutError("")}>{signOutError}</Notice>}
      {children}
    </AppShell.Main>
    {creating && <Suspense fallback={null}><NewDashboard onClose={closeNew} onCreated={id => { setCreating(false); void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId: id }, search: {} }); }} /></Suspense>}
  </AppShell>;
}

/* Exported for shell.test.tsx: reaching it through Shell would drag in the
   router, the app context and a viewer. */
function AccountMenu({ onError }: { onError: (message: string) => void }) {
  const navigate = useNavigate();
  const viewer = useViewer();
  const label = viewer.display_name.trim() || viewer.email.trim() || "?";
  const initial = label.charAt(0).toUpperCase();
  return <Menu position="bottom-end" withinPortal shadow="md">
    <Menu.Target>
      <ActionIcon variant="subtle" color="gray" size="lg" radius="xl" aria-label={`${initial} account menu`}>
        <Avatar size={26} radius="xl" color="gray">{initial}</Avatar>
      </ActionIcon>
    </Menu.Target>
    <Menu.Dropdown>
      <Menu.Label>{viewer.email || "Signed in"}</Menu.Label>
      {/* Account actions only. This menu carried two outbound marketing links
          and no way to reach the workspace's own settings, so the one thing an
          operator came here for was the one thing missing. */}
      <Menu.Item leftSection={<PlugsConnected size={15} />} onClick={() => void navigate({ to: "/settings" })}>Connect telemetry</Menu.Item>
      <Menu.Divider />
      <Menu.Item leftSection={<SignOut size={15} />} onClick={() => void logout().catch((cause) => onError(cause instanceof Error ? cause.message : "Sign-out failed — your session is still active."))}>Sign out</Menu.Item>
    </Menu.Dropdown>
  </Menu>;
}
