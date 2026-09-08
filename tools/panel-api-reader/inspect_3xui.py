#!/usr/bin/env python3
"""Read-only 3x-ui API inspector, focused on client payloads.

The program never mutates the panel. It reads the full inbound list and the
first-class client list, then reports the fields present on every client so
panel/node compatibility problems are easy to spot.
"""

from __future__ import annotations

import argparse
import getpass
import json
import os
import re
import ssl
import sys
from datetime import datetime, timezone
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit, urlunsplit
from urllib.request import Request, urlopen


SENSITIVE_KEYS = {
    "apiToken",
    "auth",
    "id",
    "password",
    "privateKey",
    "preSharedKey",
    "publicKey",
    "secret",
    "subId",
    "token",
    "uuid",
}


class PanelAPIError(RuntimeError):
    """An API, HTTP, transport, or response-shape error."""


def normalize_base_url(value: str) -> str:
    """Accept a panel root, /panel/inbounds, or /panel/api-docs URL."""

    raw = value.strip()
    parsed = urlsplit(raw)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ValueError("base URL must be an absolute http(s) URL")
    if parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise ValueError("base URL must not contain credentials, query, or fragment")

    path = parsed.path.rstrip("/")
    marker = re.search(r"/panel(?:/.*)?$", path)
    if marker:
        path = path[: marker.start()]
    return urlunsplit((parsed.scheme, parsed.netloc, path, "", "")).rstrip("/")


def redact(value: Any, reveal_credentials: bool) -> Any:
    """Redact credentials while preserving their field names and shapes."""

    if reveal_credentials:
        return value
    if isinstance(value, dict):
        return {
            key: "<redacted>" if key in SENSITIVE_KEYS else redact(item, False)
            for key, item in value.items()
        }
    if isinstance(value, list):
        return [redact(item, False) for item in value]
    return value


def non_empty_fields(client: dict[str, Any]) -> list[str]:
    """Return keys whose values carry a meaningful non-default value."""

    result: list[str] = []
    for key, value in client.items():
        if value is None or value is False or value == 0 or value == "":
            continue
        if isinstance(value, (list, dict)) and not value:
            continue
        result.append(key)
    return sorted(result)


def envelope_obj(payload: Any, endpoint: str) -> Any:
    if not isinstance(payload, dict):
        raise PanelAPIError(f"{endpoint}: response is not an object")
    if not payload.get("success"):
        raise PanelAPIError(f"{endpoint}: {payload.get('msg') or 'API rejected request'}")
    return payload.get("obj")


def inbound_summary(inbound: dict[str, Any]) -> dict[str, Any]:
    settings = inbound.get("settings")
    settings = settings if isinstance(settings, dict) else {}
    stream = inbound.get("streamSettings")
    stream = stream if isinstance(stream, dict) else {}
    return {
        "id": inbound.get("id"),
        "remark": inbound.get("remark"),
        "tag": inbound.get("tag"),
        "enable": inbound.get("enable"),
        "listen": inbound.get("listen"),
        "port": inbound.get("port"),
        "protocol": inbound.get("protocol"),
        "network": stream.get("network"),
        "security": stream.get("security"),
        "clientCount": len(settings.get("clients", []))
        if isinstance(settings.get("clients"), list)
        else 0,
    }


def clients_from_inbounds(inbounds: list[dict[str, Any]]) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for inbound in inbounds:
        settings = inbound.get("settings")
        if not isinstance(settings, dict) or not isinstance(settings.get("clients"), list):
            continue
        for client in settings["clients"]:
            if not isinstance(client, dict):
                continue
            rows.append(
                {
                    "inboundId": inbound.get("id"),
                    "inboundRemark": inbound.get("remark"),
                    "inboundTag": inbound.get("tag"),
                    "client": client,
                    "fields": sorted(client),
                    "nonEmptyFields": non_empty_fields(client),
                }
            )
    return rows


class PanelAPI:
    def __init__(self, base_url: str, token: str, timeout: float, insecure: bool):
        self.base_url = normalize_base_url(base_url)
        self.token = token.strip()
        if not self.token:
            raise ValueError("API token must not be empty")
        self.timeout = timeout
        self.context = ssl._create_unverified_context() if insecure else ssl.create_default_context()

    def get(self, endpoint: str) -> Any:
        url = f"{self.base_url}/panel/api/{endpoint.lstrip('/')}"
        request = Request(
            url,
            headers={
                "Accept": "application/json",
                "Authorization": f"Bearer {self.token}",
                "User-Agent": "3x-node-panel-api-reader/1.0",
            },
            method="GET",
        )
        try:
            with urlopen(request, context=self.context, timeout=self.timeout) as response:
                payload = json.load(response)
        except HTTPError as exc:
            body = exc.read().decode("utf-8", "replace")[:500]
            raise PanelAPIError(f"{endpoint}: HTTP {exc.code}: {body}") from exc
        except (URLError, TimeoutError, OSError, json.JSONDecodeError) as exc:
            raise PanelAPIError(f"{endpoint}: {exc}") from exc
        return envelope_obj(payload, endpoint)


def inspect_panel(
    client: PanelAPI,
    inbound_ids: set[int] | None,
    reveal_credentials: bool,
) -> dict[str, Any]:
    raw_inbounds = client.get("inbounds/list")
    if not isinstance(raw_inbounds, list):
        raise PanelAPIError("inbounds/list: obj is not a list")
    inbounds = [item for item in raw_inbounds if isinstance(item, dict)]
    if inbound_ids is not None:
        inbounds = [item for item in inbounds if item.get("id") in inbound_ids]

    raw_clients = client.get("clients/list")
    clients = raw_clients if isinstance(raw_clients, list) else []
    if inbound_ids is not None:
        clients = [
            item
            for item in clients
            if isinstance(item, dict)
            and any(inbound_id in inbound_ids for inbound_id in item.get("inboundIds", []))
        ]

    inbound_clients = clients_from_inbounds(inbounds)
    field_inventory: dict[str, dict[str, Any]] = {}
    for row in inbound_clients:
        client_data = row["client"]
        identity = client_data.get("email") or client_data.get("id") or "<unnamed>"
        field_inventory.setdefault(
            str(identity),
            {"fields": row["fields"], "nonEmptyFields": row["nonEmptyFields"]},
        )

    return {
        "generatedAt": datetime.now(timezone.utc).isoformat(),
        "baseURL": client.base_url,
        "inbounds": [inbound_summary(item) for item in inbounds],
        "clients": redact(clients, reveal_credentials),
        "clientsFromInboundSettings": redact(inbound_clients, reveal_credentials),
        "clientFieldInventory": field_inventory,
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True, help="Panel root or /panel/api-docs URL")
    parser.add_argument(
        "--token-env",
        default="XUI_API_TOKEN",
        help="Environment variable containing the API token (default: XUI_API_TOKEN)",
    )
    parser.add_argument("--inbound-id", type=int, action="append", help="Only inspect this inbound ID")
    parser.add_argument("--timeout", type=float, default=15.0)
    parser.add_argument("--insecure", action="store_true", help="Allow self-signed certificates")
    parser.add_argument(
        "--show-credentials",
        action="store_true",
        help="Show UUIDs/passwords/auth/secrets; redacted by default",
    )
    parser.add_argument("--output", help="Write JSON to this file instead of stdout")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    token = os.environ.get(args.token_env) or getpass.getpass("3x-node API token: ")
    try:
        panel = PanelAPI(args.base_url, token, args.timeout, args.insecure)
        result = inspect_panel(panel, set(args.inbound_id) if args.inbound_id else None, args.show_credentials)
        encoded = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
        if args.output:
            with open(args.output, "w", encoding="utf-8") as handle:
                handle.write(encoded)
        else:
            sys.stdout.write(encoded)
        return 0
    except (PanelAPIError, ValueError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
