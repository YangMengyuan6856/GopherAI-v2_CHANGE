# GopherAI DevSupport MCP boundary

This module keeps the MCP transport boundary used by the current container
topology and by the future governed Tool Runtime adapter.

The old demo tools were deliberately retired:

- weather and current time;
- arithmetic calculator;
- unrestricted generic web search and page fetch.

They demonstrated protocol connectivity but did not support the product's
project-knowledge and incident-diagnosis scenario. The MCP server currently
exposes no business tools. Start the protocol host with:

```bash
go run . -mode server
```

New tools must be scenario-specific and must not be exposed directly to an
Agent. They first need the shared Tool Runtime gates defined by the SDD:

```text
discover -> authorize -> validate -> secure -> budget -> execute
         -> retry/circuit/cache -> audit -> metrics
```

The first planned allowlisted capabilities are read-only service-health
inspection and restricted official-document search/fetch. The generic client
package remains as a protocol adapter; it contains no tool-specific helper.
