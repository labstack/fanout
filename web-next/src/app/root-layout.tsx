import { Outlet } from "react-router";
import { Rail } from "./rail";

export function RootLayout() {
  return (
    <div className="flex min-h-screen">
      <Rail />
      <main className="flex-1 px-10 py-8 max-w-[1000px]">
        <Outlet />
      </main>
    </div>
  );
}
