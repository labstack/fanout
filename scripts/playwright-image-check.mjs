import { readFileSync } from "node:fs";
const image = JSON.parse(readFileSync("/ms-playwright/.docker-info", "utf8"));
const driver = JSON.parse(readFileSync(new URL("../ui/host/node_modules/@playwright/test/package.json", import.meta.url), "utf8"));
if (image.driverVersion !== driver.version) throw new Error(`Playwright image driver ${image.driverVersion} differs from installed ${driver.version}`);
console.log(`Playwright image and driver match: ${driver.version}`);
