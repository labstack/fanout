import { NavLink } from "react-router";
import { House, StackSimple, ChatCircleText, Bell } from "@phosphor-icons/react";
import { cn } from "@/shared/lib/cn";

const items = [
  { to: "/", label: "Home", Icon: House, end: true },
  { to: "/services", label: "Services", Icon: StackSimple, end: false },
  { to: "/investigate", label: "Investigate", Icon: ChatCircleText, end: false },
  { to: "/alerts", label: "Alerts", Icon: Bell, end: false },
];

export function Rail() {
  return (
    <nav aria-label="Primary" className="w-[220px] shrink-0 border-r border-line p-3.5 flex flex-col gap-1">
      <div className="px-2 pb-5 pt-1.5 font-semibold tracking-tight">Fanout</div>
      {items.map(({ to, label, Icon, end }) => (
        <NavLink
          key={to}
          to={to}
          end={end}
          className={({ isActive }) =>
            cn(
              "flex items-center gap-3 rounded-lg px-2.5 py-2 font-medium text-ink-2 hover:bg-raised-2 hover:text-ink",
              isActive && "bg-raised-2 text-ink",
            )
          }
        >
          <Icon size={18} />
          {label}
        </NavLink>
      ))}
    </nav>
  );
}
