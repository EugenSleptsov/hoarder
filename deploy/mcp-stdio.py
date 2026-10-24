#!/usr/bin/env python3
"""Bridges MCP over stdio to hoarder's loopback HTTP endpoint.

A client on another machine runs it through SSH, so it needs neither a tunnel nor a local copy of the token:
    ssh -o BatchMode=yes user@server python3 bots/hoarder/deploy/mcp-stdio.py
Each stdin line is one JSON-RPC message; each response is one stdout line. Errors never include the token.
"""
import argparse
import json
import os
import sys
import urllib.error
import urllib.request


def reply_error(message, text):
    """Answers a request the endpoint could not answer, so the client does not wait forever."""
    try:
        msg = json.loads(message)
    except ValueError:
        return None
    if not isinstance(msg, dict) or "method" not in msg or msg.get("id") is None:
        return None
    return json.dumps({"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32603, "message": text}})


def forward(url, token, message):
    req = urllib.request.Request(url, data=message.encode("utf-8"), method="POST", headers={
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json",
        "Accept": "application/json",
    })
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            body = resp.read().decode("utf-8").strip()
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", "replace").strip()
        try:
            json.loads(body)
            return body
        except ValueError:
            return reply_error(message, "hoarder MCP endpoint answered HTTP %d" % e.code)
    except (urllib.error.URLError, OSError) as e:
        reason = getattr(e, "reason", e)
        return reply_error(message, "hoarder MCP endpoint unavailable: %s" % reason)
    return body or None


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--url", default="http://127.0.0.1:8765/mcp")
    parser.add_argument("--token-file", default=os.path.expanduser("~/bots/hoarder-secrets/mcp-token"))
    args = parser.parse_args()
    with open(args.token_file, encoding="utf-8") as f:
        token = f.read().strip()
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        out = forward(args.url, token, line)
        if out:
            sys.stdout.write(out.replace("\n", " ") + "\n")
            sys.stdout.flush()


if __name__ == "__main__":
    main()
