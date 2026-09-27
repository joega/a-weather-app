"""Verify release gate rejection of unsafe ELF program-header combinations."""
from pathlib import Path
import runpy
import unittest
from unittest.mock import patch

check = runpy.run_path(str(Path(__file__).resolve().parents[1] /
                          "packaging/check_hardening.py"))["check"]
HEADERS = """
 LOAD 0x0 0x0 0x0 0x100 0x100 R E 0x1000
 GNU_STACK 0x0 0x0 0x0 0x0 0x0 RW 0x10
 GNU_RELRO 0x100 0x100 0x100 0x100 0x100 R 0x1
"""
DYNAMIC = " 0x000000000000001e (FLAGS) BIND_NOW\n 0x000000006ffffffb (FLAGS_1) NOW PIE\n"


class HardeningTests(unittest.TestCase):
    def test_accepts_protected_executable(self):
        with patch("subprocess.check_output", side_effect=[HEADERS, DYNAMIC]):
            check("host")

    def test_rejects_missing_and_executable_memory_protections(self):
        cases = ((HEADERS.replace("GNU_RELRO", "IGNORED"), DYNAMIC),
                 (HEADERS, DYNAMIC.replace("BIND_NOW", "IGNORED").replace("NOW PIE", "PIE")),
                 (HEADERS.replace("RW 0x10", "RWE 0x10"), DYNAMIC),
                 (HEADERS.replace("R E 0x1000", "RWE 0x1000"), DYNAMIC),
                 (HEADERS, DYNAMIC.replace("PIE", "IGNORED")))
        for headers, dynamic in cases:
            with self.subTest(headers=headers, dynamic=dynamic):
                with patch("subprocess.check_output", side_effect=[headers, dynamic]):
                    with self.assertRaises(ValueError):
                        check("host")

    def test_unrelated_dynamic_strings_do_not_count_as_flags(self):
        fake = " 0x000000000000000e (SONAME) Library soname: [BIND_NOW NOW PIE]\n"
        with patch("subprocess.check_output", side_effect=[HEADERS, fake]):
            with self.assertRaises(ValueError):
                check("host")

    def test_now_flags_one_alone_is_full_relro(self):
        with patch("subprocess.check_output", side_effect=[HEADERS,
                " 0x000000006ffffffb (FLAGS_1) NOW PIE\n"]):
            check("host")
