import { expect, test } from "@playwright/test";
import snapshot from "../fixtures/catalog-snapshot.json" with { type: "json" };
import history from "../fixtures/history-expected.json" with { type: "json" };

for (const path of [
  "./",
  "./collections/",
  "./methodology/",
  "./contributing/",
]) {
  test(`mobile navigation and skip link remain usable on ${path}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 320, height: 800 });
    await page.goto(path);
    await page.keyboard.press("Tab");
    const skip = page.getByRole("link", { name: "Skip to content" });
    await expect(skip).toBeFocused();
    await expect(skip).toBeInViewport();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("main")).toBeFocused();
    const navigation = page.getByRole("navigation", {
      name: "Main navigation",
    });
    for (const link of await navigation
      .getByRole("link", { includeHidden: true })
      .all()) {
      await expect(link).toBeVisible();
      await link.focus();
      await expect(link).toBeFocused();
      expect(
        await link.evaluate(
          (element) => getComputedStyle(element).outlineStyle,
        ),
      ).not.toBe("none");
    }
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
  });
}

test("saved result and historical tables can scroll by keyboard at narrow widths", async ({
  page,
}) => {
  const saved = structuredClone(snapshot);
  saved.result.repository.full_name = "kubernetes/kubernetes";
  const series = structuredClone(history);
  series.repository.full_name = "kubernetes/kubernetes";
  for (const month of series.months)
    month.repository.full_name = "kubernetes/kubernetes";
  await page.route("**/data/kubernetes--kubernetes.json", (route) =>
    route.fulfill({ json: saved }),
  );
  await page.route("**/data/history/kubernetes--kubernetes.json", (route) =>
    route.fulfill({ json: series }),
  );
  await page.route("**/data/**/index.json", (route) =>
    route.fulfill({ status: 404 }),
  );
  await page.route("**/data/index.json", (route) =>
    route.fulfill({ status: 404 }),
  );
  await page.setViewportSize({ width: 320, height: 800 });
  await page.goto("./?repo=kubernetes/kubernetes");
  await expect(page.locator("#history-data")).toBeVisible();
  for (const name of [
    "Snapshot PR categories",
    "Review participation table",
    "Historical PR categories",
    "Monthly review activity table",
  ]) {
    const region = page.getByRole("region", { name, exact: true });
    await region.focus();
    await expect(region).toBeFocused();
    expect(
      await region.evaluate(
        (element) => getComputedStyle(element).outlineStyle,
      ),
    ).not.toBe("none");
  }
  const monthly = page.getByRole("region", {
    name: "Monthly review activity table",
    exact: true,
  });
  await page.keyboard.press("ArrowRight");
  await expect
    .poll(() => monthly.evaluate((element) => element.scrollLeft))
    .toBeGreaterThan(0);
  await page.keyboard.press("Tab");
  await expect(monthly).not.toBeFocused();
  const tip = page.getByRole("button", { name: "Explain Share", exact: true });
  const bounds = await tip.boundingBox();
  expect(bounds?.width).toBeGreaterThanOrEqual(24);
  expect(bounds?.height).toBeGreaterThanOrEqual(24);
  await tip.focus();
  await expect(page.locator("#metric-help-7")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(tip).toBeFocused();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
});
