"""Qualify an exact staged candidate with an explicit Windows or WSL Go toolchain.

Example (repeat with each declared minimum and the corresponding targets):
  python qualification/platform_matrix.py --lane <stage> --platform windows
      --go "C:/Program Files/Go/bin/go.exe" --target all --mode all
  python qualification/platform_matrix.py --lane <stage> --platform linux
      --go /mnt/c/.../go/bin/go --target shared-nav --target adapter --mode normal

Every invocation creates new evidence; failed runs are never overwritten. Tests
use private candidate archives, not published dependency qualification. Linux
root checks cover the explicit server package set; use root-navigation for the
focused runtime gate. Race is a separate cgo-enabled diagnostic, and missing C
toolchains are recorded as unavailable rather than silently passing.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time

sys.dont_write_bytecode = True
from candidate_inputs import verify as verify_candidate


ROOT = Path("C:/Demiurgos/navmesh-validation/world-test")
TARGETS = {
    "library": "../navmesh-go",
    "library-worldgen": "../navmesh-go",
    "root": "",
    "root-navigation": "",
    "shared-nav": "golem/nav",
    "adapter": "golem/nav/navmesh",
    "demo": "examples/navigation-server",
    "stress": "examples/navigation-stress",
}
DEFAULT_TARGETS = [name for name in TARGETS if name not in ("root-navigation", "library", "library-worldgen")]
MODES = ("normal", "vet", "checkptr", "race")
LINUX_SERVER_PACKAGES = ["./cmd/golem-bake", "./schema/...", "./codegen/...", "./golem/...", "./golem-go-client/...", "./internal/..."]
ALLOCATION_SKIP = "/.*Allocations$"


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def confined(path, root=ROOT):
    path, root = Path(path).resolve(), Path(root).resolve()
    if path != root and root not in path.parents:
        raise ValueError(f"path escapes validation workspace: {path}")
    return path


def linux_path(path):
    text = str(path).replace("\\", "/")
    if text.startswith("/"):
        return text
    if re.match(r"^[A-Za-z]:/", text):
        return "/mnt/" + text[0].lower() + "/" + text[3:]
    raise ValueError(f"WSL path must be absolute: {path}")


def go_quote(value):
    # Go's -exec value uses quoted.Split, not a shell and not POSIX escapes.
    value = str(value).replace("\\", "/")
    if '"' in value or "'" in value:
        raise ValueError("quotes are unsupported in test-exec paths")
    return '"' + value + '"'


def host_toolchain_path(executable):
    match = re.match(r"^/mnt/([A-Za-z])/(.*)$", str(executable))
    return Path(match[1] + ":/" + match[2]) if match else Path(executable)


def split_go_words(value):
    """Parse Go command fields without treating Windows backslashes as escapes."""
    words = []
    while value := value.lstrip(" \t\r\n"):
        if value[0] in "\"'":
            end = value.find(value[0], 1)
            if end < 0:
                raise ValueError("unterminated compiler command quote")
            words.append(value[1:end])
            value = value[end + 1:]
        else:
            match = re.match(r"[^ \t\r\n]+", value)
            words.append(match[0])
            value = value[match.end():]
    return words


def compiler_command(value, platform):
    value = value.strip()
    # An existing unquoted Windows executable can itself contain spaces.
    if platform == "windows" and Path(value).is_file():
        return [Path(value).resolve().as_posix()]
    words = split_go_words(value)
    if not words or not words[0]:
        raise ValueError("empty compiler command")
    if platform == "windows":
        executable = shutil.which(words[0])
        if executable:
            words[0] = Path(executable).resolve().as_posix()
    return words


def join_go_words(words):
    result = []
    for word in words:
        if word and not re.search(r"[ \t\r\n\"']", word):
            result.append(word)
        elif "'" not in word:
            result.append("'" + word + "'")
        elif '"' not in word:
            result.append('"' + word + '"')
        else:
            raise ValueError("compiler argument contains both quote kinds")
    return " ".join(result)


def expand(values, all_values):
    result = []
    for value in values or ["all"]:
        for item in all_values if value == "all" else [value]:
            if item not in result:
                result.append(item)
    return result


def required_version(module):
    match = re.search(r"^go\s+(\d+)\.(\d+)(?:\.(\d+))?\s*$", (module / "go.mod").read_text(), re.MULTILINE)
    if not match:
        raise ValueError(f"missing Go language floor: {module}")
    return tuple(int(part or 0) for part in match.groups())


def verify_sources(lane, manifest):
    # The shared check also rejects unmanifested source-tree files.
    return [item["path"] + ": " + item["reason"] for item in verify_candidate(lane)["errors"]]


class Matrix:
    def __init__(self, args):
        self.args = args
        self.cc = compiler_command(args.cc or "gcc", args.platform)
        self.root = ROOT.resolve()
        self.lane = confined(args.lane)
        self.manifest = json.loads((self.lane / "manifest.json").read_text())
        self.stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
        self.output = confined(args.output or self.root / "runs" / f"platform-{self.stamp}")
        self.output.mkdir(parents=True, exist_ok=False)
        self.scratch = confined(self.root / "scratch" / f"platform-{self.stamp}")
        self.scratch.mkdir(parents=True, exist_ok=False)
        self.bin = confined(self.root / "bin" / self.lane.name / args.platform / self.stamp)
        self.bin.mkdir(parents=True, exist_ok=False)
        # Candidate version strings are reused between snapshots: their module
        # caches must be isolated by stage, even when the compiler cache is shared.
        self.modules = self.root / "cache" / self.lane.name / f"platform-{args.platform}-modules"
        self.build = self.root / "cache" / f"platform-{args.platform}-build"
        for path in (self.modules, self.build):
            path.mkdir(parents=True, exist_ok=True)
        helper = Path(__file__).with_name("platform_test_exec.py")
        self.helper = self.scratch / helper.name
        shutil.copyfile(helper, self.helper)
        self.results = []
        self.meta = {
            "started_utc": self.stamp,
            "qualification": "unpublished private candidate archives; not published dependency resolution",
            "stage": str(self.lane),
            "source_manifest_sha256": sha256(self.lane / "manifest.json"),
            "runner_sha256": sha256(__file__),
            "input_verifier_sha256": sha256(Path(__file__).with_name("candidate_inputs.py")),
            "test_exec_sha256": sha256(self.helper),
            "requested_argv": sys.argv,
            "platform": args.platform,
            "distro": args.distro if args.platform == "linux" else None,
            "bin_root": str(self.bin),
            "retained_binaries": "direct package tests; generated child go commands retain their own normal test behavior",
            "status": "running",
        }
        compiler = host_toolchain_path(args.go)
        self.meta["toolchain_executable"] = {"requested": args.go, "sha256": sha256(compiler) if compiler.is_file() else None}
        if args.platform == "linux" and not args.no_prefill:
            self.meta["dependency_prefill_executable"] = {"requested": args.prefill_go, "sha256": sha256(args.prefill_go) if Path(args.prefill_go).is_file() else None, "role": "dependency extraction only; not compiler under test"}
        self.write()

    def write(self):
        (self.output / "results.json").write_text(json.dumps({**self.meta, "checks": self.results}, indent=2) + "\n", encoding="utf-8")

    def path(self, path):
        return linux_path(path) if self.args.platform == "linux" else str(path)

    def environment(self, mode, host=False):
        path = str if host else self.path
        proxy = (self.lane / "proxy").as_uri() if host or self.args.platform == "windows" else "file://" + self.path(self.lane / "proxy")
        env = {
            "GOWORK": "off", "GOTOOLCHAIN": "local", "GOENV": "off", "GOEXPERIMENT": "", "GOTELEMETRY": "off", "CGO_ENABLED": "1" if mode == "race" and not host else "0",
            "GOFLAGS": "-mod=mod", "GOPROXY": proxy + ",https://proxy.golang.org",
            "GONOSUMDB": "github.com/demiurgos-hub/navmesh-go,github.com/demiurgos-hub/golem-engine*",
            "GOMODCACHE": path(self.modules), "GOCACHE": path(self.build),
            "GOTMPDIR": path(self.scratch), "TEMP": path(self.scratch), "TMP": path(self.scratch), "TMPDIR": path(self.scratch),
        }
        executable = self.args.prefill_go if host else self.args.go
        if host or self.args.platform == "windows":
            env["PATH"] = str(Path(executable).resolve().parent) + os.pathsep + os.environ.get("PATH", "")
        else:
            env["PATH"] = self.path(executable).rsplit("/", 1)[0] + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
        if not host and "architecture" in self.meta:
            env["GOOS"], env["GOARCH"] = self.args.platform, self.meta["architecture"]
        if not host:
            env["CC"] = join_go_words(self.cc)
        return env

    def run(self, name, command, cwd, environment, host=False, **details):
        if self.args.pause_file:
            pause = confined(self.args.pause_file)
            while pause.exists():
                time.sleep(1)
        log = self.output / (name + ".log")
        if self.args.platform == "linux" and not host:
            actual = ["wsl", "-d", self.args.distro, "--cd", self.path(cwd), "--exec", "env", *[f"{k}={v}" for k, v in environment.items()], *command]
            process_env = os.environ.copy()
        else:
            actual = command
            process_env = {**os.environ, **environment}
            # A shell's cross-compilation settings must not silently qualify the
            # wrong architecture or leak into generated child-module tests.
            for key in ("GOOS", "GOARCH", "GOMOD", "GOWORK"):
                if key not in environment:
                    process_env.pop(key, None)
        record = {"name": name, "argv": actual, "cwd": str(cwd), "environment": environment, "log": str(log), "status": "running", **details}
        self.results.append(record)
        self.write()
        started = time.monotonic()
        try:
            with log.open("w", encoding="utf-8") as stream:
                result = subprocess.run(actual, cwd=cwd, env=process_env, stdout=stream, stderr=subprocess.STDOUT, check=False)
            record.update(exit_code=result.returncode, status="passed" if result.returncode == 0 else "failed")
        except OSError as error:
            log.write_text(repr(error) + "\n", encoding="utf-8")
            record.update(exit_code=None, status="unavailable", error=repr(error))
        except BaseException as error:
            record.update(status="interrupted", error=repr(error))
            raise
        finally:
            record["seconds"] = time.monotonic() - started
            self.write()
        print(name, record["status"], flush=True)
        return record, log.read_text(encoding="utf-8", errors="replace")

    def unavailable(self, name, reason):
        self.results.append({"name": name, "status": "unavailable", "reason": reason})
        self.write()

    def execute(self):
        initial_errors = verify_sources(self.lane, self.manifest)
        self.meta["source_hash_mismatches_before"] = initial_errors
        if initial_errors:
            raise ValueError("staged source differs from its manifest; capture a fresh lane")
        targets = expand(self.args.target, DEFAULT_TARGETS)
        modes = expand(self.args.mode, MODES)
        ordinary_env = self.environment("normal")
        go = self.path(self.args.go)
        version_record, version_text = self.run("toolchain-version", [go, "version"], self.lane, ordinary_env)
        match = re.search(r"go version go(\d+)\.(\d+)(?:\.(\d+))? (\w+)/(\w+)", version_text)
        if version_record["status"] != "passed" or not match or match[4] != self.args.platform:
            raise ValueError("explicit toolchain failed or is for a different execution platform")
        version = tuple(int(part or 0) for part in match.groups()[:3])
        self.meta.update(go_version=".".join(map(str, version)), architecture=match[5])
        ordinary_env = self.environment("normal")
        self.run("toolchain-environment", [go, "env", "-json", "GOOS", "GOARCH", "GOVERSION", "GOTOOLCHAIN", "CC"], self.lane, ordinary_env)
        race_available = True
        if "race" in modes:
            cc = self.cc
            check, _ = self.run("race-c-compiler", [*cc, "--version"], self.lane, self.environment("race"))
            race_available = check["status"] == "passed"
            if race_available:
                identity_script = "import hashlib,json,pathlib,shutil,sys; p=pathlib.Path(shutil.which(sys.argv[1]) or sys.argv[1]).resolve(); print(json.dumps({'requested':sys.argv[1],'resolved':str(p),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}))"
                python = "/usr/bin/python3" if self.args.platform == "linux" else sys.executable
                identity, identity_text = self.run("race-c-compiler-identity", [python, "-c", identity_script, cc[0]], self.lane, self.environment("race"))
                race_available = identity["status"] == "passed"
                if race_available:
                    self.meta["race_c_compiler"] = {**json.loads(identity_text), "argv": cc, "go_cc_environment": join_go_words(cc)}
                    self.write()
        for target in targets:
            module = confined(self.lane / "golem-engine" / TARGETS[target], self.lane)
            minimum = required_version(module)
            if version < minimum:
                self.unavailable(target, f"Go {version} is below declared module floor {minimum}; no automatic toolchain switch")
                continue
            modfile = self.scratch / (target + ".mod")
            shutil.copyfile(module / "go.mod", modfile)
            if (module / "go.sum").exists():
                shutil.copyfile(module / "go.sum", modfile.with_suffix(".sum"))
            # Windows extracts dependency ZIPs on NTFS much faster than WSL.
            # This does not select the compiler that executes the Linux checks.
            if self.args.platform == "linux" and not self.args.no_prefill:
                prefill, _ = self.run(target + "-dependency-prefill", [self.args.prefill_go, "mod", "download", "-modfile=" + str(modfile)], module, self.environment("normal", host=True), host=True)
                if prefill["status"] != "passed":
                    continue
            packages = ["./..."]
            exclusions = []
            if target == "root" and self.args.platform == "linux":
                packages = LINUX_SERVER_PACKAGES
                exclusions = ["root ./... replaced by explicit server packages; golem-ebiten client graphics requires cgo/system graphics dependencies"]
            if target == "root-navigation":
                packages = ["./golem"]
                exclusions = ["focused navigation runtime gate; not broad server or networking qualification"]
            if target == "library-worldgen":
                packages = ["./qualification/worldgen"]
                exclusions = ["focused workload-fixture checks; production library qualification is recorded separately"]
            for mode in modes:
                name = target + "-" + mode
                if mode == "race" and not race_available:
                    self.unavailable(name, "requested race gate has no working C compiler; no execution qualification")
                    continue
                flags = ["vet"] if mode == "vet" else ["test", "-count=1", "-timeout=10m"]
                flags += ["-modfile=" + self.path(modfile)]
                if mode in ("checkptr", "race"):
                    flags += ["-skip=" + ALLOCATION_SKIP]
                    flags += ["-gcflags=all=-d=checkptr=2"] if mode == "checkptr" else ["-race"]
                if mode != "vet":
                    python = "/usr/bin/python3" if self.args.platform == "linux" else sys.executable
                    executor = [python, self.path(self.helper), "--validation-root", self.path(self.root), "--bin-root", self.path(self.bin / target / mode), "--source-root", self.path(module), "--"]
                    flags += ["-exec=" + " ".join(go_quote(value) for value in executor)]
                    if target == "root-navigation":
                        flags += ["-run=Navigation"]
                self.run(name, [go, *flags, *packages], module, self.environment(mode), target=target, mode=mode, packages=packages, exclusions=exclusions, allocation_assertions="skipped named /.*Allocations$ diagnostic subtests only" if mode in ("checkptr", "race") else "ordinary assertions included" if mode == "normal" else "not applicable")
            self.meta.setdefault("external_modfiles", {})[target] = {"path": str(modfile), "sha256": sha256(modfile), "sum_sha256": sha256(modfile.with_suffix(".sum")) if modfile.with_suffix(".sum").exists() else None}
            self.write()
        self.meta["source_hash_mismatches_after"] = verify_sources(self.lane, self.manifest)
        success = not self.meta["source_hash_mismatches_after"] and all(check["status"] == "passed" for check in self.results)
        self.meta["status"] = "passed" if success else "failed"
        self.meta["completed_utc"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        self.write()
        return 0 if success else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--lane", required=True, help="exact stage.py workspace with manifest.json and private proxy")
    parser.add_argument("--output", help="new evidence directory below the validation root; must not exist")
    parser.add_argument("--platform", choices=("windows", "linux"), required=True)
    parser.add_argument("--distro", default="Ubuntu", help="WSL distribution used for native Linux execution")
    parser.add_argument("--target", choices=(*TARGETS, "all"), action="append", help="repeat; default engine targets; library, library-worldgen and root-navigation are explicit")
    parser.add_argument("--mode", choices=(*MODES, "all"), action="append", help="repeat; default all, including the separate race diagnostic")
    parser.add_argument("--go", required=True, help="explicit compiler executable; no automatic toolchain switching")
    parser.add_argument("--cc", help="C compiler for the optional race lane")
    parser.add_argument("--prefill-go", default=shutil.which("go"), help="Windows Go used only to extract Linux dependency modules")
    parser.add_argument("--no-prefill", action="store_true", help="let Linux Go download/extract dependencies directly")
    parser.add_argument("--pause-file", help="existing file pauses between commands; never suspends an active test")
    args = parser.parse_args()
    if os.name != "nt":
        parser.error("run this Windows-host orchestration script from Windows; Linux tests execute natively through WSL")
    if args.platform == "linux" and not args.no_prefill and not args.prefill_go:
        parser.error("--prefill-go is required when Windows Go is not on PATH, or use --no-prefill")
    matrix = Matrix(args)
    try:
        return matrix.execute()
    except BaseException as error:
        matrix.meta.update(status="interrupted" if isinstance(error, KeyboardInterrupt) else "failed", error=repr(error))
        matrix.meta["source_hash_mismatches_after"] = verify_sources(matrix.lane, matrix.manifest)
        matrix.write()
        raise
    finally:
        print(matrix.output, flush=True)


if __name__ == "__main__":
    raise SystemExit(main())
