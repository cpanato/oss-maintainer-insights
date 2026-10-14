# Accessibility and mobile checks

The interface uses native links, buttons, form labels, table headers and captions. Navigation remains available on narrow screens. Each page has a skip link whose main-content target accepts focus without adding a tab stop. Scrollable data tables are named keyboard-focusable regions with visible focus indicators. Tooltip buttons have at least 24-by-24 CSS-pixel targets and retain focus when Escape dismisses the explanation.

## Local verification

Run the README Node checks, then `npm run test:browser`. For the focused regression scenarios, run `npx playwright test tests/browser/accessibility.spec.ts`. Mandatory browser checks use synthetic saved datasets and do not require live GitHub access or secrets.

The scenarios exercise all four pages at 320 CSS pixels, visible navigation links, keyboard skip-link activation, focus transfer, focus outlines, page reflow, saved category tables, monthly-table scrolling and leaving the scroll region with Tab. Existing scenarios cover mobile query submission, cancellation, pagination, tooltip mouse/keyboard/touch interactions and Escape. Collections has its own keyboard-scroll and viewport checks.

Data tables retain their two-dimensional structure inside horizontally scrollable containers. Inspect the page at desktop and mobile sizes, and confirm scroll hints, readable dates and intact styles with real published data before release. Browser checks cover these interactions; assistive-technology reading order and announcements also need manual review.

## Reference

Use the W3C guidance for [keyboard focus visibility](https://www.w3.org/WAI/WCAG22/Understanding/focus-visible), [reflow and scrollable data tables](https://www.w3.org/WAI/WCAG22/Understanding/reflow), and [table header semantics](https://www.w3.org/WAI/tutorials/tables/).
