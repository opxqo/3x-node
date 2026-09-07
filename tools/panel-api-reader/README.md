# 3x-ui API reader

This is a standalone, read-only Python program for inspecting a 3x-ui panel.
It focuses on client payloads and reports the exact field names returned by the
panel, which is useful when diagnosing panel-to-node compatibility issues.

It calls only:

- `GET /panel/api/inbounds/list`
- `GET /panel/api/clients/list`

It never creates, updates, deletes, or synchronizes anything.

## Usage

```sh
export XUI_API_TOKEN='paste-token-in-your-own-terminal'
python3 tools/panel-api-reader/inspect_3xui.py \
  --base-url 'https://panel.example.com:8443/panel/api-docs' \
  --insecure \
  --output panel-clients.json
```

The program redacts UUIDs, passwords, `auth`, `secret`, subscription IDs and
key material by default. Add `--show-credentials` only when writing a local
diagnostic file that must contain those values. Do not paste that file into
chat or commit it.

To inspect only one inbound, repeat the filter when needed:

```sh
python3 tools/panel-api-reader/inspect_3xui.py \
  --base-url 'https://panel.example.com:8443' \
  --inbound-id 81
```
