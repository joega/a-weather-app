"""Desktop audit wrapper checks with fake Docker; never starts a container."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


WRAPPER = Path(__file__).resolve().parents[1] / "scripts/run_docker_audit.sh"


class DockerWrapperTests(unittest.TestCase):
    def check_status(self, status):
        with tempfile.TemporaryDirectory() as folder:
            fake = Path(folder) / "docker"
            fake.write_text('#!/usr/bin/bash\n'
                'if [[ "$1" == info ]]; then exit 0; fi\n'
                'printf "fake Docker invocation:"\n'
                'printf " %s" "$@"\n'
                'printf "\\n"\n'
                f'exit {status}\n')
            fake.chmod(0o700)
            env = dict(os.environ, PATH=folder + os.pathsep + os.environ["PATH"])
            result = subprocess.run(["/usr/bin/bash", str(WRAPPER)], env=env,
                capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, status, result.stderr)
            self.assertIn(f"Audit exit status: {status}", result.stdout)
            logs = [line.removeprefix("Log file: ") for line in result.stdout.splitlines()
                    if line.startswith("Log file: ")]
            self.assertEqual(len(set(logs)), 1)
            log = Path(logs[-1])
            try:
                self.assertEqual(log.stat().st_mode & 0o777, 0o600)
                contents = log.read_text()
                self.assertIn("dst=/source,readonly", contents)
                self.assertIn("--rm", contents)
                self.assertIn("bash /source/packaging/audit-container.sh", contents)
                self.assertNotIn("fake Docker invocation", result.stdout)
            finally:
                log.unlink()

    def test_success_preserves_zero_status_and_private_log(self):
        self.check_status(0)

    def test_failure_preserves_nonzero_status_and_private_log(self):
        self.check_status(7)


if __name__ == "__main__":
    unittest.main()
