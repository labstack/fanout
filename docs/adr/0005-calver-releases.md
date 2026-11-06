# 0005. CalVer releases

Status: Accepted
Date: 2026-10-30

## Context

Fanout ships frequently and its maturity is better described by date and stage
than by semantic version numbers.

## Decision

Releases use CalVer `YYYY.M.N[-alpha|-beta|-rc]`: full year, unpadded month,
and a release number within the month that starts at 0 and increments for
every published release. The suffix states maturity; GA has none. Git tags
carry a leading `v`; container tags do not. Published versions are immutable.

## Consequences

- A version tells when it was built and how mature it is.
- Product versions, client-contract versions and protocol versions are kept
  separate; the product version never appears in API paths or tool names.
