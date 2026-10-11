# Repository conventions

- Write repository artifacts in English, including documentation, code comments, commit/PR text, and product strings.
- Implement scheduled collection and its GitHub Actions execution in Go. Use TypeScript for browser queries and the website.
- Maintain shared JSON fixtures and Go/TypeScript metric parity whenever methodology changes.
- When uncertain about an external library choice, consult the project owner with alternatives and tradeoffs before adopting it.
- Run the checks documented in the README for the affected components. Keep mandatory PR tests independent of live API access and secrets.
