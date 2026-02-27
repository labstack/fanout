import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Components } from "react-markdown";
import { replaceEmojis } from "@/lib/emoji-icons";

function processChildren(children: React.ReactNode): React.ReactNode {
  if (typeof children === "string") return replaceEmojis(children);
  if (Array.isArray(children)) return children.map(processChildren);
  return children;
}

const components: Components = {
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

export function Markdown({ children }: { children: string }) {
  return (
    <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
      {children}
    </ReactMarkdown>
  );
}
