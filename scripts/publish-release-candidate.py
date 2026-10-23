"""Publish an existing release-candidate draft using its tested CI package."""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import zipfile


REPOSITORY = "eclipse-vlr/pc-club-admin"
FILES = {
    "start-container.cmd",
    "scripts/start-container.ps1",
    "scripts/container-excel.ps1",
    "scripts/container-update.ps1",
    "deploy/compose.staging.yaml",
    "deploy/compose.excel-test.yaml",
    "release-image.txt",
}


def require(condition, message):
    if not condition:
        raise SystemExit(message)


def api(path, payload=None):
    command = ["gh", "api", f"repos/{REPOSITORY}/{path}"]
    if payload is not None:
        command += ["--method", "PATCH", "--input", "-"]
    return json.loads(subprocess.check_output(
        command, input=json.dumps(payload).encode() if payload is not None else None
    ))


def download(path, destination):
    with destination.open("wb") as output:
        command = ["gh", "api", f"repos/{REPOSITORY}/{path}"]
        if path.startswith("releases/assets/"):
            command += ["--header", "Accept: application/octet-stream"]
        subprocess.run(command, stdout=output, check=True)


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--verify-only", action="store_true")
    args = parser.parse_args()
    require(re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+-rc\.[1-9][0-9]*", args.tag),
            "Only release-candidate tags are supported")
    require(re.fullmatch(r"[1-9][0-9]*", args.run_id), "Invalid CI run ID")

    run = api(f"actions/runs/{args.run_id}")
    require(run["repository"]["full_name"] == REPOSITORY
            and run["event"] == "push" and run["head_branch"] == "main"
            and run["path"] == ".github/workflows/ci.yml"
            and run["status"] == "completed" and run["conclusion"] == "success",
            "A successful main push CI run is required")
    revision = run["head_sha"]
    require(re.fullmatch(r"[0-9a-f]{40}", revision), "Invalid CI commit")
    jobs = api(f"actions/runs/{args.run_id}/jobs?per_page=100")["jobs"]
    for name in ("foundation", "windows-launcher", "publish-image"):
        matching = [job for job in jobs if job["name"] == name]
        require(len(matching) == 1 and matching[0]["conclusion"] == "success",
                f"CI job must succeed: {name}")

    drafts = [item for item in api("releases?per_page=100")
              if item["tag_name"] == args.tag]
    require(len(drafts) == 1, "One existing release draft is required")
    release = drafts[0]
    require(release["draft"] and release["prerelease"]
            and release["target_commitish"] == revision and release["body"],
            "An existing prerelease draft targeting the tested commit is required")
    artifacts = api(f"actions/runs/{args.run_id}/artifacts?per_page=100")["artifacts"]
    candidates = [item for item in artifacts
                  if item["name"] == f"PC-Club-Admin-Windows-{revision}"]
    require(len(candidates) == 1 and not candidates[0]["expired"],
            "One unexpired Windows artifact is required")
    artifact = candidates[0]

    with tempfile.TemporaryDirectory(prefix="pcclub-release-") as directory:
        root = Path(directory)
        archive = root / f"PC-Club-Admin-Windows-{args.tag}.zip"
        download(f"actions/artifacts/{artifact['id']}/zip", archive)
        checksum = sha256(archive)
        require(artifact.get("digest") == "sha256:" + checksum,
                "CI artifact SHA-256 mismatch")
        with zipfile.ZipFile(archive) as package:
            require(len(package.namelist()) == len(FILES)
                    and set(package.namelist()) == FILES and package.testzip() is None,
                    "Unexpected or corrupt files in the package")
            for name in sorted(FILES - {"release-image.txt"}):
                source = api(f"contents/{name}?ref={revision}")
                require(source["encoding"] == "base64"
                        and package.read(name) == base64.b64decode(source["content"]),
                        f"Package differs from the tested source: {name}")
            image = package.read("release-image.txt").decode().strip()
            require(re.fullmatch(
                r"ghcr\.io/eclipse-vlr/pc-club-admin@sha256:[0-9a-f]{64}", image),
                "The package must pin an image digest")

        manifest = root / "SHA256SUMS.txt"
        manifest.write_text(f"{checksum}  {archive.name}\n", encoding="utf-8")
        expected = {archive.name: archive, manifest.name: manifest}
        print(f"Verified {args.tag}: commit {revision}, ZIP SHA-256 {checksum}")
        if args.verify_only:
            return

        assets = api(f"releases/{release['id']}/assets?per_page=100")
        require(len({item['name'] for item in assets}) == len(assets)
                and {item['name'] for item in assets} <= expected.keys(),
                "Unexpected release assets; refusing to publish")
        for item in assets:
            downloaded = root / f"existing-{item['id']}"
            download(f"releases/assets/{item['id']}", downloaded)
            require(sha256(downloaded) == sha256(expected[item["name"]]),
                    "Existing release asset differs; refusing to overwrite")
        present = {item["name"] for item in assets}
        missing = [str(path) for name, path in expected.items() if name not in present]
        if missing:
            subprocess.run(["gh", "release", "upload", args.tag, *missing,
                            "--repo", REPOSITORY], check=True)

        assets = api(f"releases/{release['id']}/assets?per_page=100")
        require(len(assets) == len(expected)
                and {item['name'] for item in assets} == expected.keys(),
                "Release assets are incomplete")
        for item in assets:
            downloaded = root / f"uploaded-{item['id']}"
            download(f"releases/assets/{item['id']}", downloaded)
            require(sha256(downloaded) == sha256(expected[item["name"]]),
                    "Uploaded asset SHA-256 mismatch")

        current = api(f"releases/{release['id']}")
        require(current["draft"] and current["prerelease"]
                and current["target_commitish"] == revision
                and current["body"] == release["body"],
                "Draft changed while preparing the release")
        published = api(f"releases/{release['id']}", {
            "draft": False, "prerelease": True, "make_latest": "false",
        })
        require(not published["draft"] and published["prerelease"],
                "Release was not published as a prerelease")
        print(published["html_url"])
        if os.environ.get("GITHUB_STEP_SUMMARY"):
            with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as summary:
                summary.write(f"Published [{args.tag}]({published['html_url']})\n\n"
                              f"ZIP SHA-256: `{checksum}`\n\nImage: `{image}`\n")


if __name__ == "__main__":
    main()
