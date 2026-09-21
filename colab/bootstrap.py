"""Small, finite-lifetime adapter for starting ColabNomad from Colab."""

import argparse
import hashlib
import json
import os
import platform
import stat
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path


NETWORK_TIMEOUT = 45


class BootstrapError(RuntimeError):
    pass


@dataclass(frozen=True)
class BootstrapConfig:
    target_repo: str
    target_ref: str = ""
    release: str = ""
    state_dir: Path = Path("/content/.colabnomad")


def platform_key():
    machine = platform.machine().lower()
    if machine in ("x86_64", "amd64"):
        return "linux-amd64"
    if machine in ("aarch64", "arm64"):
        return "linux-arm64"
    raise BootstrapError("unsupported Linux machine: " + machine)


def load_versions(repo_root):
    path = Path(repo_root) / "config" / "versions.json"
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        raise BootstrapError("cannot read config/versions.json") from exc


def _digest_spec(integrity):
    try:
        algorithm, expected = integrity.split(":", 1)
        hashlib.new(algorithm)
    except (ValueError, TypeError):
        raise BootstrapError("invalid integrity value")
    if len(expected) != hashlib.new(algorithm).digest_size * 2:
        raise BootstrapError("invalid integrity value")
    return algorithm, expected.lower()


def download_verified(url, integrity, dst):
    algorithm, expected = _digest_spec(integrity)
    destination = Path(dst)
    destination.parent.mkdir(parents=True, exist_ok=True)
    digest = hashlib.new(algorithm)
    try:
        with urllib.request.urlopen(url, timeout=NETWORK_TIMEOUT) as response, destination.open("wb") as output:
            while True:
                chunk = response.read(1024 * 1024)
                if not chunk:
                    break
                digest.update(chunk)
                output.write(chunk)
    except urllib.error.HTTPError:
        destination.unlink(missing_ok=True)
        raise
    except OSError as exc:
        destination.unlink(missing_ok=True)
        raise BootstrapError("download failed") from exc
    if digest.hexdigest().lower() != expected:
        destination.unlink(missing_ok=True)
        raise BootstrapError("download checksum mismatch")
    return destination


def _release_name():
    return "colabnomad-" + platform_key()


def _checksum_for(checksums, name):
    for line in checksums.decode("utf-8", "strict").splitlines():
        fields = line.split()
        if len(fields) >= 2 and fields[-1].lstrip("*") == name:
            return fields[0]
    raise BootstrapError("release checksum does not name " + name)


def try_release(config, repo_root):
    if not config.release:
        return None
    name = _release_name()
    base = "https://github.com/pedroteste00000008-stack/ColabNomad/releases/download/" + config.release
    try:
        with urllib.request.urlopen(base + "/SHA256SUMS", timeout=NETWORK_TIMEOUT) as response:
            checksums = response.read()
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            return None
        raise BootstrapError("release lookup failed") from exc
    except OSError as exc:
        raise BootstrapError("release lookup failed") from exc
    expected = _checksum_for(checksums, name)
    config.state_dir.mkdir(parents=True, exist_ok=True)
    target_dir = config.state_dir / "bin"
    with tempfile.TemporaryDirectory(dir=str(config.state_dir)) as tmp:
        try:
            downloaded = download_verified(base + "/" + name, "sha256:" + expected, Path(tmp) / name)
        except urllib.error.HTTPError as exc:
            if exc.code == 404:
                return None
            raise
        target_dir.mkdir(parents=True, exist_ok=True)
        target = target_dir / "colabnomad"
        downloaded.replace(target)
    target.chmod(target.stat().st_mode | stat.S_IXUSR)
    return target


def _safe_extract(archive, destination):
    destination = Path(destination).resolve()
    with tarfile.open(archive, "r:gz") as tar:
        for member in tar.getmembers():
            if member.issym() or member.islnk():
                raise BootstrapError("Go archive contains a link")
            target = (destination / member.name).resolve()
            if target != destination and destination not in target.parents:
                raise BootstrapError("unsafe Go archive path")
        tar.extractall(destination, filter="data")


def build_checked_out(config, repo_root):
    release = try_release(config, repo_root)
    if release is not None:
        return release
    versions = load_versions(repo_root)
    try:
        artifact = versions["bootstrap_go"]["artifacts"][platform_key()]
        version = versions["bootstrap_go"]["version"]
    except KeyError as exc:
        raise BootstrapError("missing pinned bootstrap Go artifact") from exc
    toolchain = config.state_dir / "toolchains" / ("go-" + version)
    go = toolchain / "bin" / "go"
    if not go.exists():
        config.state_dir.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=str(config.state_dir)) as tmp:
            archive = download_verified(artifact["url"], artifact["integrity"], Path(tmp) / "go.tgz")
            toolchain.parent.mkdir(parents=True, exist_ok=True)
            extract_dir = Path(tmp) / "extract"
            extract_dir.mkdir()
            _safe_extract(archive, extract_dir)
            extracted = extract_dir / "go"
            if not extracted.is_dir():
                raise BootstrapError("Go archive has no go directory")
            extracted.replace(toolchain)
    output = config.state_dir / "bin" / "colabnomad"
    output.parent.mkdir(parents=True, exist_ok=True)
    env = {key: os.environ[key] for key in ("HOME", "PATH", "LANG", "LC_ALL", "SHELL", "TERM", "TMPDIR") if key in os.environ}
    env.update({"CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": platform_key().split("-", 1)[1]})
    subprocess.run(
        [str(go), "build", "-trimpath", "-ldflags", "-s -w", "-o", str(output), "./cmd/colabnomad"],
        cwd=str(repo_root), env=env, check=True,
    )
    return output


def _userdata_get(name):
    try:
        from google.colab import userdata
        return userdata.get(name)
    except (ImportError, KeyError, RuntimeError):
        return None


def collect_colab_secrets():
    secrets = {}
    for name in ("GITHUB_TOKEN", "OPENCODE_API_KEY"):
        value = _userdata_get(name)
        if value:
            secrets[name] = value
    return secrets


def run_up(binary, config, secrets, repo_root=None):
    argv = [str(binary), "up", "--state-dir", str(config.state_dir), "--repo", config.target_repo]
    if config.target_ref:
        argv.extend(("--ref", config.target_ref))
    binary_dir = str(Path(binary).resolve().parent)
    env = {key: os.environ[key] for key in ("HOME", "PATH", "LANG", "LC_ALL", "SHELL", "TERM", "TMPDIR") if key in os.environ}
    env["PATH"] = binary_dir + os.pathsep + env.get("PATH", "")
    checkout = Path(repo_root) if repo_root is not None else Path(__file__).resolve().parents[1]
    env["COLABNOMAD_VERSIONS_FILE"] = str(checkout / "config" / "versions.json")
    env["COLABNOMAD_STATE_DIR"] = str(config.state_dir)
    env.update(secrets)
    return subprocess.run(argv, env=env, check=False).returncode


def main(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument("--target-repo", required=True)
    parser.add_argument("--target-ref", default="")
    parser.add_argument("--release", default="")
    parser.add_argument("--state-dir", type=Path, default=Path("/content/.colabnomad"))
    args = parser.parse_args(argv)
    config = BootstrapConfig(args.target_repo, args.target_ref, args.release, args.state_dir)
    repo_root = Path(__file__).resolve().parents[1]
    binary = build_checked_out(config, repo_root)
    return run_up(binary, config, collect_colab_secrets(), repo_root)


if __name__ == "__main__":
    sys.exit(main())
