"""Assess or install the approved Ubuntu race-instrumentation C toolchain.

Run through WSL Python; installation needs the explicitly supplied --install
flag and root. The approved dependency versions below are intentionally pinned:
changed simulations require a new review, never an implicit package upgrade.
Example: wsl -d Ubuntu -u root --exec python3 /mnt/c/.../install_linux_race_compiler.py --install
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


ROOT = Path("/mnt/c/Demiurgos/navmesh-validation/world-test")
EXPECTED = {
    "gcc-15-base": "15.2.0-16ubuntu1", "libisl23": "0.27-1build1", "libmpc3": "1.3.1-3",
    "cpp-15-x86-64-linux-gnu": "15.2.0-16ubuntu1", "cpp-15": "15.2.0-16ubuntu1",
    "cpp-x86-64-linux-gnu": "4:15.2.0-5ubuntu1", "cpp": "4:15.2.0-5ubuntu1",
    "libcc1-0": "16-20260322-1ubuntu1", "libgomp1": "16-20260322-1ubuntu1",
    "libitm1": "16-20260322-1ubuntu1", "libasan8": "16-20260322-1ubuntu1",
    "liblsan0": "16-20260322-1ubuntu1", "libtsan2": "16-20260322-1ubuntu1",
    "libubsan1": "16-20260322-1ubuntu1", "libhwasan0": "16-20260322-1ubuntu1",
    "libquadmath0": "16-20260322-1ubuntu1", "libgcc-15-dev": "15.2.0-16ubuntu1",
    "gcc-15-x86-64-linux-gnu": "15.2.0-16ubuntu1", "gcc-15": "15.2.0-16ubuntu1",
    "gcc-x86-64-linux-gnu": "4:15.2.0-5ubuntu1", "gcc": "4:15.2.0-5ubuntu1",
    "libc-dev-bin": "2.43-2ubuntu2", "linux-libc-dev": "7.0.0-14.14",
    "rpcsvc-proto": "1.4.3-1build1", "libc6-dev": "2.43-2ubuntu2",
}


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--install", action="store_true")
    args = parser.parse_args()
    if not ROOT.is_dir():
        parser.error("the fixed validation workspace is unavailable")
    if args.install and os.geteuid() != 0:
        parser.error("installation requires the explicitly selected WSL root user")
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
    output = ROOT / "runs" / ("linux-race-compiler-" + stamp)
    output.mkdir(parents=True, exist_ok=False)
    scratch = ROOT / "scratch" / ("linux-race-compiler-" + stamp)
    scratch.mkdir(parents=True, exist_ok=False)
    cache = ROOT / "cache" / "apt"
    archives = cache / "archives"
    for path in (archives / "partial", output / "apt"):
        path.mkdir(parents=True, exist_ok=True)
    environment = {**os.environ, "LC_ALL": "C", "DEBIAN_FRONTEND": "noninteractive", "TMPDIR": str(scratch), "TMP": str(scratch), "TEMP": str(scratch)}
    evidence = {"created_utc": stamp, "runner_sha256": digest(__file__), "requested_install": args.install, "expected_new_packages": EXPECTED, "checks": [], "status": "running"}
    def save():
        (output / "manifest.json").write_text(json.dumps(evidence, indent=2) + "\n")
    def run(name, command):
        log = output / (name + ".log")
        entry = {"name": name, "argv": command, "cwd": str(scratch), "log": str(log), "status": "running"}
        evidence["checks"].append(entry)
        save()
        with log.open("w") as stream:
            result = subprocess.run(command, cwd=scratch, env=environment, stdout=stream, stderr=subprocess.STDOUT, check=False)
        entry.update(exit_code=result.returncode, status="passed" if result.returncode == 0 else "failed")
        save()
        if result.returncode:
            raise RuntimeError(f"{name} failed; inspect {log}")
        return log.read_text()
    def inventory(name):
        text = run(name, ["dpkg-query", "-W", "-f=${binary:Package}\t${Version}\t${db:Status-Abbrev}\n"])
        return {parts[0]: parts[1] for line in text.splitlines() if len(parts := line.split("\t")) == 3 and parts[2].startswith("ii")}
    apt = ["apt-get", "-o", "Dir::Cache=" + str(cache), "-o", "Dir::Cache::archives=" + str(archives), "-o", "Dir::Cache::pkgcache=" + str(cache / "pkgcache.bin"), "-o", "Dir::Cache::srcpkgcache=" + str(cache / "srcpkgcache.bin"), "-o", "Dir::Log=" + str(output / "apt"), "-o", "DPkg::Options::=--log=" + str(output / "dpkg.log")]
    packages = ["gcc=" + EXPECTED["gcc"], "libc6-dev=" + EXPECTED["libc6-dev"]]
    try:
        before = inventory("packages-before")
        simulation = run("simulation", [*apt, "-s", "--no-install-recommends", "--no-upgrade", "--no-remove", "install", *packages])
        selected = dict(re.findall(r"^Inst (\S+) \((\S+)", simulation, re.MULTILINE))
        evidence["simulation_packages"] = selected
        if selected != EXPECTED or "0 upgraded, 25 newly installed, 0 to remove" not in simulation or re.search(r"^Remv ", simulation, re.MULTILINE):
            raise RuntimeError("simulation differs from the approved package set; no installation attempted")
        if not args.install:
            evidence["status"] = "assessment-only"
            return 0
        run("install", [*apt, "-y", "--no-install-recommends", "--no-upgrade", "--no-remove", "install", *packages])
        after = inventory("packages-after")
        added = {name: version for name, version in after.items() if name not in before}
        changed = {name: [version, after.get(name)] for name, version in before.items() if after.get(name) != version}
        evidence.update(added_packages=added, changed_or_removed_packages=changed)
        if {name.split(":")[0]: version for name, version in added.items()} != EXPECTED or changed:
            raise RuntimeError("installed package delta differs from the approved set; preserve evidence and reassess")
        evidence["archives"] = [{"path": str(path), "sha256": digest(path), "bytes": path.stat().st_size} for path in sorted(archives.glob("*.deb"))]
        gcc = shutil.which("gcc")
        if not gcc:
            raise RuntimeError("installed compiler is unavailable")
        run("compiler-version", [gcc, "--version"])
        binaries = {"gcc": gcc, "cc": shutil.which("cc")}
        for name in ("cc1", "as", "ld"):
            value = run("compiler-" + name, [gcc, "-print-prog-name=" + name]).strip()
            binaries[name] = value if Path(value).is_absolute() else shutil.which(value)
        evidence["binaries"] = {}
        for name, value in binaries.items():
            if value is None:
                raise RuntimeError(f"compiler helper missing: {name}")
            path = Path(value).resolve()
            evidence["binaries"][name] = {"requested": value, "resolved": str(path), "sha256": digest(path)}
        evidence["status"] = "installed-and-verified"
        return 0
    except BaseException as error:
        evidence.update(status="failed", error=repr(error))
        raise
    finally:
        save()
        print(output, flush=True)


if __name__ == "__main__":
    raise SystemExit(main())
