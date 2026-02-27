import emojiRegex from "emoji-regex";
import type { LucideIcon } from "lucide-react";
import {
  Circle,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  TrendingUp,
  TrendingDown,
  Flame,
  Clock,
  Zap,
  Bell,
  Search,
  Mail,
  Shield,
  Activity,
  Siren,
} from "lucide-react";

type EmojiDef = { icon: LucideIcon; className: string };

const emojiMap = new Map<string, EmojiDef>([
  // Status circles
  ["🔴", { icon: Circle, className: "text-red-400 fill-red-400" }],
  ["🟡", { icon: Circle, className: "text-amber-400 fill-amber-400" }],
  ["🟢", { icon: Circle, className: "text-emerald-400 fill-emerald-400" }],
  ["🔵", { icon: Circle, className: "text-blue-400 fill-blue-400" }],
  ["🟠", { icon: Circle, className: "text-orange-400 fill-orange-400" }],
  ["⚪", { icon: Circle, className: "text-zinc-400 fill-zinc-400" }],
  // Check/cross
  ["✅", { icon: CheckCircle2, className: "text-emerald-400" }],
  ["❌", { icon: XCircle, className: "text-red-400" }],
  // Signals
  ["⚠️", { icon: AlertTriangle, className: "text-amber-400" }],
  ["📈", { icon: TrendingUp, className: "text-emerald-400" }],
  ["📉", { icon: TrendingDown, className: "text-red-400" }],
  ["🔥", { icon: Flame, className: "text-orange-400" }],
  ["⏱️", { icon: Clock, className: "text-muted-foreground" }],
  ["⚡", { icon: Zap, className: "text-amber-400" }],
  ["🔔", { icon: Bell, className: "text-blue-400" }],
  ["🔍", { icon: Search, className: "text-muted-foreground" }],
  ["📧", { icon: Mail, className: "text-blue-400" }],
  ["🛡️", { icon: Shield, className: "text-emerald-400" }],
  ["💓", { icon: Activity, className: "text-red-400" }],
  ["🚨", { icon: Siren, className: "text-red-400" }],
]);

let regex: RegExp;
try {
  regex = emojiRegex();
} catch {
  console.error("[emoji-icons] failed to compile emoji regex, replacement disabled");
  regex = /(?!)/; // never-matching fallback
}

/**
 * Replace mapped emojis with Lucide icons; unmapped emojis pass through as text.
 * Always returns a non-empty array (returns [text] when no emojis are found).
 */
export function replaceEmojis(text: string): React.ReactNode[] {
  try {
    const result: React.ReactNode[] = [];
    let lastIndex = 0;

    for (const match of text.matchAll(regex)) {
      const emoji = match[0];
      const index = match.index;
      if (index === undefined) continue;

      if (index > lastIndex) {
        result.push(text.slice(lastIndex, index));
      }

      // Strip variation selector (U+FE0F) for lookup — some LLMs emit bare forms
      const def = emojiMap.get(emoji) ?? emojiMap.get(emoji.replace(/\uFE0F/g, ""));
      if (def) {
        const Icon = def.icon;
        result.push(
          <Icon
            key={index}
            className={`inline-block h-3.5 w-3.5 align-middle ${def.className}`}
            style={{ transform: "translateY(-1px)" }}
          />,
        );
      } else {
        result.push(emoji);
      }

      lastIndex = index + emoji.length;
    }

    if (lastIndex < text.length) {
      result.push(text.slice(lastIndex));
    }

    return result.length > 0 ? result : [text];
  } catch (err) {
    console.error("[emoji-icons] replaceEmojis failed, returning raw text:", err);
    return [text];
  }
}
