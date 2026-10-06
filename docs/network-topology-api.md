# Read-only network topology API v1

`GET <basePath>/panel/api/server/networkTopology` uses existing Bearer authentication (or verified mTLS on the full panel). Response envelope `success/obj`, where obj is `{version:1,sources:[{guid,parentGuid?,name?,local,status,capturedAt,config?}]}`. Exactly one `local` source; each successful config contains sanitized inbounds/outbounds/routing only. No client settings or credentials.

The full panel recursively invokes this GET using stored node credentials, with bounded depth/trail, timeout, concurrency and response budgets. Each forwarding hop records explicit `n<nodeId>-` inbound aliases and verifies the child's local GUID. The lightweight node uses its actual config builder without starting/probing the core. No maintenance, counter sampling, state write or restart occurs.

Config snapshots describe saved relationships, not verified active core state. Unknown references/conditional choices/final exit IP remain unknown. Old nodes without this GET stay unsupported; no fallback to `getConfigJson`. Monitor scope cannot read config. Node-sync (full panel) and AN-managed (lightweight node) gain only this read route, not a generic forwarding interface.

Install leaf nodes before relays/master; rebuild with the repository's existing build command and publish in a separate maintenance window. This source change does not deploy itself or migrate business data. Revert its independent commit and restore the previous binary to roll back.
