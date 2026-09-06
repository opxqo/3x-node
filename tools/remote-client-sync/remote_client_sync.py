#!/usr/bin/env python3
"""Sync clients from an official 3x-ui SQLite DB to slim remote nodes.

This is deliberately an external adapter: it does not modify the official
panel or its database. It reads the panel DB and uses the node API to converge
missing clients on inbounds assigned to a node.
"""
import argparse
import json
import sqlite3
import ssl
import sys
import time
import urllib.request


def rows(db):
    # The official DB is WAL-backed. The caller mounts the directory read-only.
    con = sqlite3.connect(db, timeout=5)
    con.row_factory = sqlite3.Row
    try:
        nodes = con.execute(
            "SELECT id,name,scheme,address,port,base_path,api_token "
            "FROM nodes NOT INDEXED WHERE enable=1 AND api_token<>''"
        ).fetchall()
        inbounds = con.execute(
            "SELECT id,node_id,tag,port FROM inbounds NOT INDEXED "
            "WHERE node_id IS NOT NULL AND enable=1"
        ).fetchall()
        clients = con.execute(
            "SELECT c.*,ci.inbound_id FROM clients c NOT INDEXED "
            "JOIN client_inbounds ci NOT INDEXED ON ci.client_id=c.id "
            "JOIN inbounds i NOT INDEXED ON i.id=ci.inbound_id "
            "WHERE i.node_id IS NOT NULL AND i.enable=1"
        ).fetchall()
        return nodes, inbounds, clients
    finally:
        con.close()


def call(node, method, path, body=None):
    base = f"{node['scheme']}://{node['address']}:{node['port']}"
    base += node["base_path"] or "/"
    url = base.rstrip("/") + "/" + path.lstrip("/")
    data = None if body is None else json.dumps(body, separators=(",", ":")).encode()
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Authorization", "Bearer " + node["api_token"])
    if data is not None:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, context=ssl._create_unverified_context(), timeout=10) as res:
        result = json.load(res)
    if not result.get("success"):
        raise RuntimeError(result.get("msg") or "node API rejected request")
    return result.get("obj")


def wire_client(row):
    # Do not send raw SQLite names: the node's strict decoder rejects fields
    # such as traffic_reset_day. These are the official Client JSON names.
    names = {
        "uuid":"id", "email":"email", "security":"security",
        "password":"password", "flow":"flow", "reverse":"reverse",
        "auth":"auth", "wg_private_key":"privateKey",
        "wg_public_key":"publicKey", "wg_allowed_ips":"allowedIPs",
        "wg_pre_shared_key":"preSharedKey", "wg_keep_alive":"keepAlive",
        "wg_forwarded_ports":"forwardedPorts", "secret":"secret",
        "ad_tag":"adTag", "limit_ip":"limitIp", "total_gb":"totalGB",
        "expiry_time":"expiryTime", "enable":"enable", "tg_id":"tgId",
        "sub_id":"subId", "group_name":"group", "comment":"comment",
        "reset":"reset", "reset_day":"resetDay", "reset_max":"resetMax",
        "traffic_reset":"trafficReset", "traffic_reset_day":"trafficResetDay",
        "created_at":"created_at", "updated_at":"updated_at",
    }
    out = {names[k]: row[k] for k in names if k in row.keys() and row[k] is not None}
    if "enable" in out:
        out["enable"] = bool(out["enable"])
    return out


def sync_once(db):
    nodes, inbounds, clients = rows(db)
    node_by_id = {n["id"]: n for n in nodes}
    ib_by_id = {i["id"]: i for i in inbounds}
    wanted = {}
    for client in clients:
        ib = ib_by_id.get(client["inbound_id"])
        if ib and ib["node_id"] in node_by_id:
            wanted.setdefault(ib["node_id"], {}).setdefault(ib["tag"], []).append(client)

    checked = added = 0
    for node_id, by_tag in wanted.items():
        node = node_by_id[node_id]
        remote = call(node, "GET", "panel/api/inbounds/list") or []
        by_remote_tag = {ib.get("tag"): ib for ib in remote if ib.get("tag")}
        for tag, desired in by_tag.items():
            local_ib = next((ib for ib in inbounds if ib["tag"] == tag), None)
            remote_ib = by_remote_tag.get(tag)
            if not remote_ib and local_ib:
                remote_ib = next((ib for ib in remote if ib.get("port") == local_ib["port"]), None)
            if not remote_ib:
                print(f"skip node={node['name']} inbound={tag}: not found", flush=True)
                continue
            existing = {c.get("email") for c in (remote_ib.get("settings") or {}).get("clients", [])}
            for client in desired:
                checked += 1
                if client["email"] in existing:
                    continue
                call(node, "POST", "panel/api/clients/add", {
                    "client": wire_client(client), "inboundIds": [remote_ib["id"]]
                })
                existing.add(client["email"])
                added += 1
                print(f"synced node={node['name']} inbound={tag} email={client['email']}", flush=True)
    return checked, added


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", default="/main-db/x-ui.db")
    parser.add_argument("--interval", type=int, default=5)
    parser.add_argument("--once", action="store_true")
    args = parser.parse_args()
    while True:
        try:
            checked, added = sync_once(args.db)
            print(f"sync checked={checked} added={added}", flush=True)
        except Exception as exc:
            print(f"sync error: {exc}", file=sys.stderr, flush=True)
        if args.once:
            return
        time.sleep(max(1, args.interval))


if __name__ == "__main__":
    main()
