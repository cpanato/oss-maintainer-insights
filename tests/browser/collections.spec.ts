import { expect, test } from "@playwright/test";
import catalog from "../../catalog/repositories.json" with { type: "json" };

test("collection overview exposes every catalog project without live API access", async ({
  page,
}) => {
  const api: string[] = [];
  page.on("request", (request) => {
    if (request.url().startsWith("https://api.github.com/"))
      api.push(request.url());
  });
  await page.goto("./");
  await page
    .getByRole("link", { name: "View collection progress for all projects" })
    .click();
  await expect(
    page.getByRole("heading", { name: "Collection status", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("not a live view of running jobs", { exact: false }),
  ).toBeVisible();
  for (const name of ["PR creation cohorts", "Historical review activity"]) {
    const table = page.getByRole("table", {
      name: `${name}: published coverage and collection outcomes`,
    });
    await expect(table.getByRole("row")).toHaveCount(catalog.length + 1);
    for (const entry of catalog)
      await expect(
        table.getByRole("link", { name: entry.repository, exact: true }),
      ).toBeVisible();
  }
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  const scroll = page.getByRole("region", {
    name: "Historical review activity table, scroll horizontally on small screens",
  });
  await scroll.focus();
  await expect(scroll).toBeFocused();
  await page.keyboard.press("ArrowRight");
  await expect
    .poll(() => scroll.evaluate((element) => element.scrollLeft))
    .toBeGreaterThan(0);
  expect(api).toEqual([]);
});
