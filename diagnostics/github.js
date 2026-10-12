import {
  GitHubClient,
  GitHubError,
  repositoryPath,
} from "../packages/github/dist/index.js";

const document = globalThis.document;
const form = document.querySelector("#query");
const input = document.querySelector("#repository");
const run = document.querySelector("#run");
const cancel = document.querySelector("#cancel");
const status = document.querySelector("#status");
const output = document.querySelector("#report");
let controller;

function display(report) {
  output.textContent = JSON.stringify(report, null, 2);
}
function headers(page) {
  return Object.fromEntries(
    [
      "link",
      "x-ratelimit-limit",
      "x-ratelimit-remaining",
      "x-ratelimit-reset",
      "retry-after",
    ].map((name) => [name, page.headers.get(name)]),
  );
}

cancel.addEventListener("click", () => controller?.abort());
form.addEventListener("submit", (event) => {
  event.preventDefault();
  void inspect();
});

async function inspect() {
  controller = new globalThis.AbortController();
  run.disabled = true;
  cancel.disabled = false;
  status.textContent = "Checking GitHub API access…";
  const client = new GitHubClient({ maxRequests: 2 });
  const report = {
    origin: globalThis.location.origin,
    collected_at: new Date().toISOString(),
    requests_used: 0,
    checks: [],
    failure_reason: null,
  };
  try {
    const path = repositoryPath(input.value);
    const metadata = await client.get(path, controller.signal);
    report.checks.push({ endpoint: "repository", headers: headers(metadata) });
    display(report);
    const pulls = await client.get(
      `${path}/pulls?state=all&sort=created&direction=desc&per_page=1`,
      controller.signal,
    );
    if (!Array.isArray(pulls.data))
      throw new GitHubError("invalid_response", "Expected a PR list");
    report.checks.push({
      endpoint: "pull_requests",
      headers: headers(pulls),
      records_on_page: pulls.data.length,
    });
    status.textContent =
      "Diagnostic complete. Verify the visible headers in the report.";
  } catch (error) {
    report.failure_reason =
      error instanceof GitHubError ? error.reason : "invalid_response";
    status.textContent = `Diagnostic stopped: ${report.failure_reason}.`;
  } finally {
    report.requests_used = client.requestsUsed;
    display(report);
    run.disabled = false;
    cancel.disabled = true;
  }
}
