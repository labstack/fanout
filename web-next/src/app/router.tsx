import { createBrowserRouter } from "react-router";
import { RootLayout } from "./root-layout";
import { HomePage } from "@/features/home/home-page";
import { EmptyState } from "@/components/states/empty-state";

function Placeholder({ name }: { name: string }) {
  return <EmptyState title={`${name} — coming soon`} />;
}

export const router = createBrowserRouter([
  {
    element: <RootLayout />,
    children: [
      { index: true, element: <HomePage /> },
      { path: "services", element: <Placeholder name="Services" /> },
      { path: "investigate", element: <Placeholder name="Investigate" /> },
      { path: "alerts", element: <Placeholder name="Alerts" /> },
    ],
  },
]);
