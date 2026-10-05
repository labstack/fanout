import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Panel } from "../../../../panels/types";

export function TextViz({ panel }: { panel: Panel }) {
  return <div className="panel-markdown"><ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml disallowedElements={["img"]} components={{ a: ({ children, ...props }) => <a {...props} target="_blank" rel="noopener noreferrer">{children}</a> }}>{panel.content ?? ""}</ReactMarkdown></div>;
}
