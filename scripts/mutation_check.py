#!/usr/bin/env python3
"""Run reviewed fault injections in a disposable Go source copy, never in-place."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]


def classify(code, stdout, stderr):
    """Build errors, timeouts and absent test executions are NOT detected faults."""
    raw = stdout + stderr
    events = []
    for line in stdout.splitlines():
        try:
            event = json.loads(line)
            if isinstance(event, dict):
                events.append(event)
        except json.JSONDecodeError:
            pass
    failed = sorted({e["Package"] + "/" + e["Test"] for e in events
                     if e.get("Action") == "fail" and e.get("Test") and e.get("Package")})
    tests_ran = any(e.get("Action") == "run" and e.get("Test") for e in events)
    passed = any(e.get("Action") == "pass" and not e.get("Test") for e in events)
    if "test timed out" in raw:
        status = "timeout"
    elif "[build failed]" in raw or "[setup failed]" in raw:
        status = "invalid"
    elif code == 0 and passed and tests_ran:
        status = "survived"
    elif code != 0 and failed and tests_ran:
        status = "killed"
    else:
        status = "invalid"
    return {"status": status, "exit_code": code, "failed_tests": failed}


def run_tests(root, log, timeout):
    env = dict(os.environ, GOTOOLCHAIN="local")
    started = time.monotonic()
    process = subprocess.Popen(
        ["go", "test", "-json", "-count=1", "-timeout=45s", "./..."], cwd=root,
        env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        start_new_session=(os.name == "posix"))
    try:
        stdout, stderr = process.communicate(timeout=timeout)
        result = classify(process.returncode, stdout, stderr)
    except subprocess.TimeoutExpired:
        if os.name == "posix":
            os.killpg(process.pid, signal.SIGKILL)
        else:
            process.kill()
        stdout, stderr = process.communicate()
        result = {"status": "timeout", "failed_tests": []}
    log.write_text(stdout + stderr, encoding="utf-8")
    result["elapsed_seconds"] = round(time.monotonic() - started, 3)
    return result


def validate_manifest(mutants):
    if not isinstance(mutants, list) or not mutants:
        raise ValueError("mutation manifest must be a nonempty list")
    seen = set()
    for m in mutants:
        if not re.fullmatch(r"M[0-9]{2}", m["id"]) or m["id"] in seen:
            raise ValueError("invalid or duplicate mutation ID")
        seen.add(m["id"])
        path = Path(m["path"])
        if (path.is_absolute() or ".." in path.parts or path.parts[0] not in ("cmd", "internal")
                or path.suffix != ".go" or path.name.endswith("_test.go")):
            raise ValueError("mutations must target production Go files within cmd/internal")
        if not m["before"] or m["before"] == m["after"]:
            raise ValueError("mutation must change a nonempty anchor")
        if m.get("required", True):
            if not m.get("witness"):
                raise ValueError("required mutation needs a named failing-test witness")
        elif not m.get("rationale"):
            raise ValueError("diagnostic-only probes require an explicit rationale")


def accepted(mutation, result):
    if result["status"] in ("invalid", "invalid_anchor", "timeout"):
        return False
    if not mutation.get("required", True):
        return True
    witness = mutation["witness"]
    return result["status"] == "killed" and any(
        t.endswith("/" + witness) for t in result["failed_tests"])


def fingerprint(root):
    """Hash production AND test inputs; exclude credentials, inventory and vendor."""
    files = [p for name in ("cmd", "internal") for p in (root/name).rglob("*") if p.is_file()]
    files += [root/name for name in ("go.mod", "go.sum") if (root/name).exists()]
    digest = hashlib.sha256()
    for path in sorted(files):
        digest.update(path.relative_to(root).as_posix().encode())
        digest.update(b"\x00")
        digest.update(hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", type=Path, default=ROOT)
    parser.add_argument("--manifest", type=Path, default=ROOT/"tests/mutations.json")
    parser.add_argument("--output", type=Path, default=ROOT/"mutation-results")
    parser.add_argument("--only", default="", help="comma-separated case IDs")
    parser.add_argument("--timeout", type=int, default=180, help="per-command wall timeout in seconds")
    args = parser.parse_args()
    project, output = args.project.resolve(), args.output.resolve()
    if args.timeout <= 0:
        parser.error("timeout must be positive")
    manifest_bytes = args.manifest.read_bytes()
    mutants = json.loads(manifest_bytes)
    validate_manifest(mutants)
    if args.only:
        only = set(args.only.split(","))
        mutants = [m for m in mutants if m["id"] in only]
        if only != {m["id"] for m in mutants}:
            parser.error("unknown mutation ID")
    output.mkdir(parents=True, exist_ok=True)
    results = {"manifest_sha256": hashlib.sha256(manifest_bytes).hexdigest(),
               "source_and_tests_sha256": fingerprint(project),
               "go_version": subprocess.check_output(["go", "version"], text=True).strip(),
               "mutants": []}
    report = output/"report.json"
    with tempfile.TemporaryDirectory(prefix="hoarder-mutations-") as tmp:
        root = Path(tmp)
        # An allowlist deliberately excludes .git, .env, databases and private
        # deployment files. Vendored dependencies are optional for offline use.
        for name in ("go.mod", "go.sum", "cmd", "internal", "vendor"):
            source = project/name
            if source.is_dir():
                shutil.copytree(source, root/name)
            elif source.exists():
                shutil.copy2(source, root/name)
        baseline = run_tests(root, output/"baseline.jsonl", args.timeout)
        results["baseline"] = baseline
        report.write_text(json.dumps(results, indent=2) + "\n")
        if baseline["status"] != "survived":
            raise SystemExit("unmodified tests failed; refusing to score mutants")
        for m in mutants:
            target = root/m["path"]
            original = target.read_text()
            record = {"id": m["id"], "title": m["title"], "path": m["path"],
                      "required": m.get("required", True)}
            if original.count(m["before"]) != 1:
                record.update(status="invalid_anchor", failed_tests=[])
            else:
                try:
                    target.write_text(original.replace(m["before"], m["after"], 1))
                    record.update(run_tests(root, output/(m["id"]+".jsonl"), args.timeout))
                finally:
                    target.write_text(original)
            record["accepted"] = accepted(m, record)
            results["mutants"].append(record)
            report.write_text(json.dumps(results, indent=2) + "\n")
            print(m["id"], record["status"], m["title"], flush=True)
    if fingerprint(project) != results["source_and_tests_sha256"]:
        raise SystemExit("working source changed during audit; results require a fresh run")
    failures = [m["id"] for m in results["mutants"] if not m["accepted"]]
    if failures:
        raise SystemExit("mutation checks failed: " + ", ".join(failures))
    required = [m for m in results["mutants"] if m["required"]]
    print(f"Detected {len(required)} required faults; diagnostic probes remain separately visible.")


if __name__ == "__main__":
    main()
