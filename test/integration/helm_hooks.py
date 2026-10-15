#!/usr/bin/env python3
"""Exercise Helm 4 hooks in an isolated cluster with the operator and Istio ambient installed."""
import argparse
import json
import shutil
import subprocess
import tempfile
import time
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--kubeconfig", required=True)
parser.add_argument("--mode", choices=["both", "plain", "ambient"], default="both")
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]


def command(binary, *parts):
    return [binary, "--kubeconfig", args.kubeconfig, *parts]


def run(binary, *parts, data=None, check=True):
    result = subprocess.run(command(binary, *parts), input=data, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=200)
    if check and result.returncode:
        raise AssertionError(f"{binary} {' '.join(parts)}:\n{result.stdout[-5000:]}")
    return result


def get(kind, name=None, namespace=None, selector=None):
    parts = ["get", kind]
    if name:
        parts.append(name)
    if namespace:
        parts.extend(["-n", namespace])
    if selector:
        parts.extend(["-l", selector])
    result = run("kubectl", *parts, "-o", "json", check=False)
    return json.loads(result.stdout) if result.returncode == 0 else None


def apply(obj):
    run("kubectl", "apply", "-f", "-", data=json.dumps(obj))


def until(predicate, seconds=45):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(1)
    raise AssertionError("timed out waiting for lifecycle assertion")


def no_hooks(namespace, release):
    policies = get("networkpolicies", namespace=namespace,
                   selector=f"hooks.bigbang.dev/release={release}")
    return policies is not None and not policies["items"]


def ready(namespace, release):
    pkg = get("package", release, namespace)
    return pkg and pkg.get("status", {}).get("observedGeneration") == pkg["metadata"]["generation"] and any(
        c["type"] == "Ready" and c["status"] == "True" for c in pkg.get("status", {}).get("conditions", []))


version = run("helm", "version", "--short").stdout.strip()
assert version.startswith("v4."), f"Helm 4 required, found {version}"
assert get("crd", "packages.bigbang.dev"), "install the operator first"
assert get("daemonset", "ztunnel", "istio-system"), "install Istio ambient first"
run("kubectl", "apply", "-f", str(root / "test/integration/fixtures/hook-networking-dependencies.yaml"))
run("kubectl", "rollout", "status", "deployment/backend", "-n", "hook-dependencies", "--timeout=120s")

with tempfile.TemporaryDirectory(prefix="helm-hook-test-") as temporary:
    chart = Path(temporary) / "chart"
    shutil.copytree(root / "test/integration/fixtures/hook-networking", chart,
                    ignore=shutil.ignore_patterns("charts"))
    shutil.copytree(root / "helm/package-integration", chart / "charts/bigbang-operator-package")
    for ambient in (False, True):
        if args.mode != "both" and (args.mode == "ambient") != ambient:
            continue
        namespace = f"hook-test-{'ambient' if ambient else 'plain'}-{int(time.time())}"
        apply({"apiVersion": "v1", "kind": "Namespace", "metadata": {
            "name": namespace, "labels": {"istio.io/dataplane-mode": "ambient"} if ambient else {}}})
        flags = ["--set", "istio.enabled=true", "--set", "istio.ambient.enabled=true"] if ambient else []
        try:
            apply({"apiVersion": "bigbang.dev/v1alpha1", "kind": "Package", "metadata": {
                "name": "existing-release", "namespace": namespace}, "spec": {"networkPolicies": {
                    "enabled": True, "prependReleaseName": True,
                    "egress": {"defaults": {"allowInNamespace": {"enabled": False}}},
                    "ingress": {"defaults": {"allowInNamespace": {"enabled": False}}}}}})
            until(lambda: ready(namespace, "existing-release"))
            baseline = get("networkpolicies", namespace=namespace, selector="bigbang.dev/package=existing-release")
            baseline_uids = {p["metadata"]["uid"] for p in baseline["items"]}
            assert baseline_uids
            install = subprocess.Popen(command("helm", "install", "monitoring", str(chart), "-n", namespace,
                                               "--timeout", "150s", *flags), stdout=subprocess.PIPE,
                                       stderr=subprocess.STDOUT, text=True)
            try:
                until(lambda: get("job", "monitoring-admission-create", namespace) is not None)
                assert get("package", "monitoring", namespace) is None, "Package existed before pre-install finished"
                assert not no_hooks(namespace, "monitoring"), "pre-install policies missing"
                if ambient:
                    def enrolled():
                        pods = get("pods", namespace=namespace, selector="job-name=monitoring-admission-create")
                        return pods and any(p["metadata"].get("annotations", {}).get("ambient.istio.io/redirection") == "enabled"
                                            for p in pods["items"])
                    until(enrolled, seconds=20)
                output, _ = install.communicate(timeout=180)
                assert install.returncode == 0, output
            finally:
                if install.poll() is None:
                    install.terminate()
                    install.wait(timeout=10)
            until(lambda: ready(namespace, "monitoring"))
            until(lambda: no_hooks(namespace, "monitoring"))
            webhook = get("validatingwebhookconfiguration", f"{namespace}-monitoring-admission")
            assert webhook["webhooks"][0]["clientConfig"]["caBundle"], "certificate patch did not run"
            run("kubectl", "run", "unrelated", "-n", namespace, "--image=curlimages/curl:8.12.1",
                "--restart=Never", "--command", "--", "sleep", "1200")
            run("kubectl", "wait", "--for=condition=Ready", "pod/unrelated", "-n", namespace, "--timeout=60s")
            run("kubectl", "exec", "-n", namespace, "unrelated", "--", "curl", "--version")
            for url in ["https://kubernetes.default.svc/version", "http://backend.hook-dependencies.svc:8080"]:
                probe = run("kubectl", "exec", "-n", namespace, "unrelated", "--", "curl", "-sk",
                            "--connect-timeout", "2", "--max-time", "4", "-o", "/dev/null", "-w", "%{http_code}",
                            url, check=False)
                assert probe.returncode != 0 and "000" in probe.stdout, f"unrelated pod reached {url}: {probe.stdout}"
            run("helm", "upgrade", "monitoring", str(chart), "-n", namespace, "--timeout", "150s", *flags)
            until(lambda: no_hooks(namespace, "monitoring"))
            run("helm", "rollback", "monitoring", "1", "-n", namespace, "--timeout", "150s")
            until(lambda: no_hooks(namespace, "monitoring"))
            run("helm", "uninstall", "monitoring", "-n", namespace, "--timeout", "60s")
            until(lambda: no_hooks(namespace, "monitoring"), seconds=190)
            remaining = get("networkpolicies", namespace=namespace, selector="bigbang.dev/package=existing-release")
            assert baseline_uids == {p["metadata"]["uid"] for p in remaining["items"]}, "another release's policies changed"
            failed = run("helm", "install", "retry", str(chart), "-n", namespace, "--timeout", "45s",
                         "--set", "failHook=true", "--set", "operatorHooks.timeoutSeconds=30", *flags, check=False)
            assert failed.returncode != 0, "failure fixture unexpectedly succeeded"
            assert get("package", "retry", namespace) is None
            until(lambda: no_hooks(namespace, "retry"))
            run("helm", "upgrade", "--install", "retry", str(chart), "--reset-values", "-n", namespace,
                "--timeout", "150s", *flags)
            until(lambda: ready(namespace, "retry"))
            until(lambda: no_hooks(namespace, "retry"))
            print(f"PASS {namespace}: install, upgrade, rollback, uninstall, isolation, failure expiry, retry", flush=True)
        finally:
            for release in ("monitoring", "retry"):
                run("helm", "uninstall", release, "-n", namespace, "--no-hooks", check=False)
                name = f"{namespace}-{release}-admission"
                run("kubectl", "delete", "clusterrole,clusterrolebinding,validatingwebhookconfiguration",
                    name, "--ignore-not-found", check=False)
            run("kubectl", "delete", "namespace", namespace, "--wait=false", check=False)
