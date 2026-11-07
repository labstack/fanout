import type { Meta, StoryObj } from "@storybook/react-vite";
import { useArgs } from "storybook/preview-api";
import { expect, fn, waitFor } from "storybook/test";
import { useRef, type FormEvent } from "react";
import { contrastRatio } from "@labstack/ui";
import type { Message } from "@ag-ui/client";
import { FanoutAppContext } from "./app-context";
import { ChatPage } from "./chat";
import { answer, userMessage } from "./catalog/fixtures";
import { pageParameters } from "./catalog/product";

const actions = { send: fn(async () => undefined), submit: fn((event: FormEvent) => event.preventDefault()), stop: fn(), retry: fn(), reloadThread: fn(), openChat: fn(), newThread: fn(), selectThread: fn(), onInput: fn() };
type Props = typeof actions & { input: string; messages: Message[] };
const meta = {
  id: "components-chat-cards", title: "Components/Chat cards",
  args: { ...actions, input: "", messages: [userMessage, { id: "answer-001", role: "assistant", content: answer }] },
  parameters: pageParameters,
  render: function Render(args: Props) {
    const [, updateArgs] = useArgs<Props>();
    const inputRef = useRef<HTMLTextAreaElement>(null);
    return <FanoutAppContext.Provider value={{ ...args, agentAvailable: true, threadID: "commerce-review", threadMissing: false, ready: true, running: false, activity: "", stopped: false, error: "", inputRef, messageTimes: {}, setInput: value => { args.onInput(value); updateArgs({ input: value }); } }}><ChatPage /></FanoutAppContext.Provider>;
  },
} satisfies Meta<Props>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Answer: Story = {};
export const Markdown: Story = { args: { messages: [userMessage, { id: "markdown-001", role: "assistant", content: "### Latency by service\n\n| Service | p95 |\n| --- | --- |\n| checkout | 342 ms |\n| cart | 78 ms |\n| accounting | 126 ms |\n| payment | 184 ms |\n\nCheckout needs attention; the other three services are within their latency budgets." }] } };
export const Composer: Story = { args: { messages: [] }, play: async ({ canvas, userEvent }) => {
  const input = canvas.getByRole("textbox", { name: "Message Fanout" });
  let typed = "";
  for (const character of "How is checkout?") {
    await userEvent.type(input, character);
    typed += character;
    await waitFor(() => expect(input).toHaveValue(typed));
  }
  const composer = input.closest<HTMLElement>(".chat-composer-field")!;
  const style = getComputedStyle(composer);
  const hex = (color: string) => "#" + color.match(/\d+/g)!.slice(0, 3).map(channel => Number(channel).toString(16).padStart(2, "0")).join("");
  const focus = style.getPropertyValue("--labstack-focus").trim();
  // The border animates for 150 ms; assert its settled color, not a mid-transition frame.
  await waitFor(() => expect(hex(style.borderColor)).toBe(focus.toLowerCase()));
  await expect(contrastRatio(hex(style.borderColor), hex(style.backgroundColor))).toBeGreaterThanOrEqual(3);
  await waitFor(() => expect(style.boxShadow).toContain(style.borderColor));
  await expect(input).toHaveValue("How is checkout?");
  await expect(canvas.getByRole("button", { name: "Send message" })).toBeEnabled();
} };
