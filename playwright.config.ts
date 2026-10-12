import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/browser",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  ...(process.env.CI ? { workers: 2 } : {}),
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: "http://127.0.0.1:4322/",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    ...devices["Desktop Chrome"],
    ...(process.env.PLAYWRIGHT_CHROME === "1" ? { channel: "chrome" } : {}),
  },
  webServer: {
    command:
      "npm run preview --workspace @oss-maintainer-insights/web -- --port 4322 --ignore-lock",
    url: "http://127.0.0.1:4322/",
    reuseExistingServer: false,
    timeout: 30000,
  },
});
