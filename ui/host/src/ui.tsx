import * as SelectPrimitive from "@radix-ui/react-select";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import { CaretDown, Check } from "@phosphor-icons/react";
import type { ButtonHTMLAttributes, ReactNode } from "react";

type SelectOption = { value: string; label: string };

export function Button({ variant = "secondary", className = "", ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "secondary" | "quiet" }) {
  const variantClass = variant === "primary"
    ? "border-accent/40 bg-accent text-white shadow-[0_8px_22px_var(--accent-glow)] hover:bg-accent/90"
    : variant === "quiet"
      ? "border-transparent bg-transparent text-muted hover:bg-panel-raised hover:text-text"
      : "border-line bg-panel-soft text-text-soft hover:border-line-strong hover:bg-panel-raised hover:text-text";
  return <button className={`${variantClass} inline-flex min-h-10 items-center justify-center gap-2 rounded-lg border px-3 text-xs font-bold transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:pointer-events-none disabled:opacity-40 ${className}`} {...props} />;
}

export function Select({ value, onValueChange, options, label, placeholder, icon, quiet = false, className = "" }: {
  value?: string;
  onValueChange: (value: string) => void;
  options: SelectOption[];
  label: string;
  placeholder?: string;
  icon?: ReactNode;
  quiet?: boolean;
  className?: string;
}) {
  return <SelectPrimitive.Root value={value} onValueChange={onValueChange}>
    <SelectPrimitive.Trigger aria-label={label} className={`${quiet ? "border-transparent bg-transparent hover:text-text focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent" : "border-line-strong bg-field hover:border-accent hover:bg-field-hover focus:border-accent focus:ring-3 focus:ring-accent/10"} inline-flex h-10 min-w-0 items-center gap-2 whitespace-nowrap rounded-lg border px-3 text-left text-xs font-semibold text-text-soft outline-none transition data-[placeholder]:text-muted [&>svg]:shrink-0 ${className}`}>
      {icon}<SelectPrimitive.Value placeholder={placeholder} /><SelectPrimitive.Icon className="ml-auto text-muted"><CaretDown size={13} weight="bold" aria-hidden="true" /></SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
    <SelectPrimitive.Portal>
      <SelectPrimitive.Content position="popper" sideOffset={6} className="z-50 min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-xl border border-line bg-panel p-1 text-text shadow-[0_18px_55px_var(--shadow)]">
        <SelectPrimitive.Viewport>
          {options.map((option) => <SelectPrimitive.Item key={option.value} value={option.value} className="relative flex h-9 cursor-default select-none items-center rounded-lg py-0 pr-8 pl-3 text-xs text-text-soft outline-none data-[highlighted]:bg-panel-raised data-[highlighted]:text-text">
            <SelectPrimitive.ItemText>{option.label}</SelectPrimitive.ItemText><SelectPrimitive.ItemIndicator className="absolute right-2.5 text-accent"><Check size={13} weight="bold" aria-hidden="true" /></SelectPrimitive.ItemIndicator>
          </SelectPrimitive.Item>)}
        </SelectPrimitive.Viewport>
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  </SelectPrimitive.Root>;
}

export function Tooltip({ label, children }: { label: string; children: ReactNode }) {
  return <TooltipPrimitive.Root>
    <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content sideOffset={7} className="z-50 rounded-md border border-line bg-panel-raised px-2 py-1 text-[10px] font-medium text-text shadow-[0_10px_28px_var(--shadow)]">
        {label}<TooltipPrimitive.Arrow className="fill-panel-raised" />
      </TooltipPrimitive.Content>
    </TooltipPrimitive.Portal>
  </TooltipPrimitive.Root>;
}

export const TooltipProvider = TooltipPrimitive.Provider;
