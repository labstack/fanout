import { ActionIcon, Alert, AppShell, Avatar, Burger, Drawer, Group, Menu, Tooltip, useComputedColorScheme, useMantineColorScheme } from "@mantine/core";
import { useDisclosure, useMediaQuery } from "@mantine/hooks";
import { BookOpen, Moon, PlugsConnected, SignOut, Sun } from "@phosphor-icons/react";
import { useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { createDashboardPrompt, useFanoutApp } from "./app-context";
import { logout, useViewer } from "./auth";
import { BrandLockup } from "./brand";
import { docs } from "../../links";
import Rail, { type RailHandle, type RailProps } from "./rail";

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
  const [keyboardOpen, setKeyboardOpen] = useState(false);
  const [signOutError, setSignOutError] = useState("");
  const railRef = useRef<RailHandle>(null);
  // The navbar stays mounted below `md`, only collapsed, so its ref is never
  // null and cannot say which rail the viewer can see. The breakpoint can.
  const mobile = useMediaQuery("(max-width: 62em)");
  const mobileRef = useRef(mobile);
  mobileRef.current = mobile;

  // A navigation from inside the drawer should close it.
  useEffect(() => { drawer.close(); }, [pathname]);

  useEffect(() => {
    const shortcuts = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        if (mobileRef.current) { setKeyboardOpen(true); drawer.open(); } else railRef.current?.focusSearch();
      }
    };
    window.addEventListener("keydown", shortcuts);
    return () => window.removeEventListener("keydown", shortcuts);
  }, []);

  const railProps: Omit<RailProps, "ref"> = {
    agentAvailable,
    activeThreadID: isChat && !threadMissing && pathname !== "/chat" ? threadID : undefined,
    activeDashboardID: dashboardId,
    onNewChat: newThread,
    onSelectThread: selectThread,
    onDeletedThread: (deletedID) => { if (deletedID === threadID) newThread(); },
    onSelectDashboard: (id) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId: id } }),
    onCreateDashboard: () => openChat(createDashboardPrompt),
    onInvestigateService: (service) => openChat(`Investigate the ${service} service. Explain its errors and latency.`),
  };

  return <AppShell header={{ height: 52 }} navbar={{ width: 256, breakpoint: "md", collapsed: { mobile: true } }} padding={0}>
    <AppShell.Header>
      <Group h="100%" px={{ base: "sm", sm: "md" }} justify="space-between" wrap="nowrap">
        <Group gap="sm" wrap="nowrap">
          <Burger hiddenFrom="md" opened={drawerOpened} onClick={() => { setKeyboardOpen(false); drawer.toggle(); }} size="sm" aria-label={drawerOpened ? "Close navigation" : "Open navigation"} />
          <BrandLockup size="small" />
        </Group>
        <Group gap="xs" wrap="nowrap">
          {/* Documentation sits in the chrome, not in the account menu: it is
              a question someone has while working, on any page, and the menu is
              for acting on your own account. */}
          <Tooltip label="Documentation">
            <ActionIcon component="a" href={docs.home} target="_blank" rel="noopener noreferrer" variant="subtle" color="gray" aria-label="Documentation">
              <BookOpen size={17} weight="bold" />
            </ActionIcon>
          </Tooltip>
          <ColorSchemeToggle />
          <AccountMenu onError={setSignOutError} />
        </Group>
      </Group>
    </AppShell.Header>
    <AppShell.Navbar p="sm"><Rail ref={railRef} {...railProps} /></AppShell.Navbar>
    <Drawer opened={drawerOpened} onClose={drawer.close} hiddenFrom="md" size={288} padding="sm" title={<BrandLockup size="small" />} overlayProps={{ backgroundOpacity: 0.24, blur: 1 }}>
      <Rail {...railProps} autoFocusSearch={drawerOpened && keyboardOpen} />
    </Drawer>
    <AppShell.Main>
      {signOutError && <Alert color="bad" m="md" withCloseButton onClose={() => setSignOutError("")}>{signOutError}</Alert>}
      {children}
    </AppShell.Main>
  </AppShell>;
}

export function ColorSchemeToggle() {
  const { setColorScheme } = useMantineColorScheme();
  // Reading the computed scheme rather than the stored one means the button
  // offers the opposite of what is on screen even while the setting is "auto".
  const scheme = useComputedColorScheme("light", { getInitialValueInEffect: true });
  const next = scheme === "dark" ? "light" : "dark";
  // The tooltip describes what the button will do, and the click changes that,
  // so leaving it open leaves the wrong sentence hanging over the header until
  // the pointer moves. It closes on the click and comes back on the next hover.
  const [tip, setTip] = useState(false);
  return <Tooltip label={`Switch to ${next} theme`} opened={tip}>
    <ActionIcon variant="subtle" color="gray" aria-label={`Switch to ${next} theme`}
      onMouseEnter={() => setTip(true)} onMouseLeave={() => setTip(false)}
      onFocus={() => setTip(true)} onBlur={() => setTip(false)}
      onClick={() => { setTip(false); setColorScheme(next); }}>
      {scheme === "dark" ? <Sun size={17} weight="bold" /> : <Moon size={17} weight="bold" />}
    </ActionIcon>
  </Tooltip>;
}

function AccountMenu({ onError }: { onError: (message: string) => void }) {
  const navigate = useNavigate();
  const viewer = useViewer();
  const label = viewer.name.trim() || viewer.email.trim() || "?";
  const initial = label.charAt(0).toUpperCase();
  return <Menu position="bottom-end" withinPortal shadow="md">
    <Menu.Target>
      <ActionIcon variant="subtle" color="gray" size="lg" radius="xl" aria-label="Account menu">
        <Avatar size={26} radius="xl" color="brand">{initial}</Avatar>
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
