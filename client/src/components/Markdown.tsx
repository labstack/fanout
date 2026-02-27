import { Component, type ReactNode, type ErrorInfo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Components } from "react-markdown";
import { replaceEmojis } from "@/lib/emoji-icons";

/**
 * Shared Markdown renderer with GFM support and emoji-to-icon replacement.
 * Intercepts text-containing elements (p, li, td, th, strong, em) to swap
 * recognized emoji characters for Lucide SVG icons.
 */

function processChildren(children: React.ReactNode, depth = 0): React.ReactNode {
  if (depth > 20) return children;
  if (typeof children === "string") return replaceEmojis(children);
  if (Array.isArray(children))
    return children.map((c) => processChildren(c, depth + 1));
  return children; // React elements (code, a, etc.) pass through unprocessed
}

const components: Components = {
  h1: ({ children, ...props }) => (
    <h1 {...props}>{processChildren(children)}</h1>
  ),
  h2: ({ children, ...props }) => (
    <h2 {...props}>{processChildren(children)}</h2>
  ),
  h3: ({ children, ...props }) => (
    <h3 {...props}>{processChildren(children)}</h3>
  ),
  h4: ({ children, ...props }) => (
    <h4 {...props}>{processChildren(children)}</h4>
  ),
  p: ({ children, ...props }) => <p {...props}>{processChildren(children)}</p>,
  li: ({ children, ...props }) => (
    <li {...props}>{processChildren(children)}</li>
  ),
  td: ({ children, ...props }) => (
    <td {...props}>{processChildren(children)}</td>
  ),
  th: ({ children, ...props }) => (
    <th {...props}>{processChildren(children)}</th>
  ),
  strong: ({ children, ...props }) => (
    <strong {...props}>{processChildren(children)}</strong>
  ),
  em: ({ children, ...props }) => (
    <em {...props}>{processChildren(children)}</em>
  ),
};

const plugins = [remarkGfm];

class MarkdownErrorBoundary extends Component<
  { children: ReactNode; fallback: string },
  { hasError: boolean }
> {
  state = { hasError: false };

  static getDerivedStateFromError() {
    return { hasError: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("[Markdown] render failed:", error, info);
  }

  render() {
    if (this.state.hasError) {
      return (
        <pre className="text-sm text-muted-foreground whitespace-pre-wrap">
          {this.props.fallback}
        </pre>
      );
    }
    return this.props.children;
  }
}

type MarkdownProps = { children: string };

export function Markdown({ children }: MarkdownProps) {
  return (
    <MarkdownErrorBoundary fallback={children}>
      <ReactMarkdown remarkPlugins={plugins} components={components}>
        {children}
      </ReactMarkdown>
    </MarkdownErrorBoundary>
  );
}
