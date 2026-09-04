# GopherAI DevSupport MCP boundary

This module keeps the MCP transport boundary used by the current container
topology and by the future governed Tool Runtime adapter.

The old demo tools were deliberately retired:

- weather and current time;
- arithmetic calculator;
- unrestricted generic web search and page fetch.

They demonstrated protocol connectivity but did not support the product's
project-knowledge and incident-diagnosis scenario. The MCP server exposes one
fixed `deployment_manifest_source` for the backend adapter. It accepts no path
arguments and binds to container loopback by default. Public callers do not
use this source directly; the backend wraps it in a governed Tool Runtime
definition before an Agent can invoke it. Start the protocol host with:

```bash
go run . -mode server
```

New tools must be scenario-specific and must not be exposed directly to an
Agent. They first need the shared Tool Runtime gates defined by the SDD:

```text
discover -> authorize -> validate -> secure -> budget -> execute
         -> retry/circuit/cache -> audit -> metrics
```

Additional sources require their own scenario value and safety review. The
generic client package remains a protocol adapter; it contains no hidden
tool-specific authorization bypass.
