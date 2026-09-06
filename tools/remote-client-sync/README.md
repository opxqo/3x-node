# Remote client sync adapter

The official 3x-ui panel can keep a client associated with a remote inbound
without sending that client to a slim node. This standalone adapter closes
that gap without changing the official panel source or writing to its DB.

Run it on the machine that runs the official panel, with the panel DB mounted
read-only. It reads node credentials from the existing `nodes` table, matches
inbounds by tag (then port), and calls the node `clients/add` API for missing
clients. A self-signed node certificate is expected because the panel already
uses its configured certificate pin.

```sh
docker build -t 3x-ui-client-sync tools/remote-client-sync
docker run -d --restart unless-stopped --name 3x-ui-client-sync \
  -v /path/to/official/db:/main-db:ro \
  3x-ui-client-sync --db /main-db/x-ui.db --interval 5
```

The adapter is additive and idempotent. It never deletes clients that were
created directly on a node.
