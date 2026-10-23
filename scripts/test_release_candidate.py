import base64
import hashlib
import importlib.util
import io
from pathlib import Path
import unittest
from unittest.mock import patch
import zipfile


spec = importlib.util.spec_from_file_location(
    "release_candidate", Path(__file__).with_name("publish-release-candidate.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)


class ReleaseCandidateTests(unittest.TestCase):
    def setUp(self):
        self.revision = "a" * 40
        self.run = {"repository": {"full_name": publisher.REPOSITORY},
                    "event": "push", "head_branch": "main",
                    "path": ".github/workflows/ci.yml", "status": "completed",
                    "conclusion": "success", "head_sha": self.revision}
        self.release = {"id": 11, "tag_name": "v0.1.0-rc.1", "draft": True,
                        "prerelease": True, "target_commitish": self.revision,
                        "body": "Release notes"}
        self.files = {name: b"synthetic source" for name in publisher.FILES}
        self.files["release-image.txt"] = (
            "ghcr.io/eclipse-vlr/pc-club-admin@sha256:" + "b" * 64 + "\n").encode()
        self.source = dict(self.files)
        self.artifact = {"id": 22, "name": f"PC-Club-Admin-Windows-{self.revision}",
                         "expired": False}
        self.assets = []
        self.update_archive()

    def update_archive(self):
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, "w") as archive:
            for name, content in self.files.items():
                archive.writestr(name, content)
        self.archive = buffer.getvalue()
        self.artifact["digest"] = "sha256:" + hashlib.sha256(self.archive).hexdigest()

    def api(self, path, payload=None):
        self.assertIsNone(payload, "Validation must not publish a release")
        if path == "actions/runs/33":
            return self.run
        if path == "actions/runs/33/jobs?per_page=100":
            return {"jobs": [{"name": name, "conclusion": "success"} for name in
                             ("foundation", "windows-launcher", "publish-image")]}
        if path == "releases?per_page=100":
            return [self.release]
        if path == "actions/runs/33/artifacts?per_page=100":
            return {"artifacts": [self.artifact]}
        if path.startswith("contents/"):
            name = path.removeprefix("contents/").split("?")[0]
            return {"encoding": "base64", "content": base64.b64encode(
                self.source[name]).decode()}
        if path == "releases/11/assets?per_page=100":
            return self.assets
        self.fail(f"Unexpected API call: {path}")

    def download(self, path, destination):
        destination.write_bytes(self.archive if path.startswith("actions/")
                                else b"tampered uploaded asset")

    def invoke(self, verify_only=True):
        args = ["publish", "--tag", "v0.1.0-rc.1", "--run-id", "33"]
        if verify_only:
            args.append("--verify-only")
        with patch("sys.argv", args), patch.object(publisher, "api", self.api), \
                patch.object(publisher, "download", self.download), \
                patch.object(publisher.subprocess, "run") as upload, \
                patch("sys.stdout", new_callable=io.StringIO):
            try:
                publisher.main()
            finally:
                upload.assert_not_called()

    def test_verified_dry_run_does_not_upload_or_publish(self):
        self.invoke()

    def test_failed_ci_is_rejected(self):
        self.run["conclusion"] = "failure"
        with self.assertRaisesRegex(SystemExit, "successful main push"):
            self.invoke()

    def test_pull_request_artifact_is_rejected(self):
        self.run["event"] = "pull_request"
        with self.assertRaisesRegex(SystemExit, "successful main push"):
            self.invoke()

    def test_release_cannot_target_an_untested_commit(self):
        self.release["target_commitish"] = "c" * 40
        with self.assertRaisesRegex(SystemExit, "targeting the tested commit"):
            self.invoke()

    def test_corrupt_artifact_is_rejected(self):
        self.artifact["digest"] = "sha256:" + "0" * 64
        with self.assertRaisesRegex(SystemExit, "artifact SHA-256 mismatch"):
            self.invoke()

    def test_extra_private_file_is_rejected_even_with_valid_checksum(self):
        self.files["AGENTS.md"] = b"synthetic private file"
        self.update_archive()
        with self.assertRaisesRegex(SystemExit, "Unexpected or corrupt files"):
            self.invoke()

    def test_package_must_match_tested_source(self):
        self.source["start-container.cmd"] = b"another revision"
        with self.assertRaisesRegex(SystemExit, "differs from the tested source"):
            self.invoke()

    def test_existing_different_asset_prevents_publication(self):
        self.assets = [{"id": 44, "name": "SHA256SUMS.txt"}]
        with self.assertRaisesRegex(SystemExit, "refusing to overwrite"):
            self.invoke(verify_only=False)


if __name__ == "__main__":
    unittest.main()
