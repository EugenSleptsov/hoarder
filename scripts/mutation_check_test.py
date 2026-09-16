"""Checks that the fault-injection runner cannot credit empty or broken runs."""
import json
import unittest

from mutation_check import accepted, classify, validate_manifest


def events(*rows):
    return "\n".join(json.dumps(row) for row in rows)


RUN = {"Action": "run", "Package": "p", "Test": "TestContract"}
FAIL = {"Action": "fail", "Package": "p", "Test": "TestContract"}
PASS = {"Action": "pass", "Package": "p"}


class ClassificationTests(unittest.TestCase):
    def test_actual_failed_test_is_detected(self):
        r = classify(1, events(RUN, FAIL), "")
        self.assertEqual(r["status"], "killed")
        self.assertEqual(r["failed_tests"], ["p/TestContract"])

    def test_green_test_is_survivor(self):
        self.assertEqual(classify(0, events(RUN, PASS), "")["status"], "survived")

    def test_build_failure_cannot_inflate_detection(self):
        self.assertEqual(classify(1, events(RUN, FAIL), "[build failed]")["status"], "invalid")

    def test_timeout_is_not_a_detection(self):
        self.assertEqual(classify(1, events(RUN, FAIL), "panic: test timed out")["status"], "timeout")

    def test_empty_suite_is_not_a_baseline(self):
        self.assertEqual(classify(0, events(PASS), "[no test files]")["status"], "invalid")

    def test_process_error_without_assertion_is_invalid(self):
        self.assertEqual(classify(1, "", "connection error")["status"], "invalid")

    def test_unrelated_failure_does_not_satisfy_witness(self):
        case = {"required": True, "witness": "TestContract"}
        result = {"status": "killed", "failed_tests": ["p/TestOther"]}
        self.assertFalse(accepted(case, result))
        result["failed_tests"].append("p/TestContract")
        self.assertTrue(accepted(case, result))

    def test_diagnostic_probe_still_rejects_invalid_build(self):
        self.assertTrue(accepted({"required": False}, {"status": "survived"}))
        self.assertFalse(accepted({"required": False}, {"status": "invalid"}))

    def test_manifest_cannot_mutate_tests_or_escape_source_tree(self):
        case = {"id": "M01", "path": "internal/item/item.go", "before": "old", "after": "new", "witness": "TestContract"}
        validate_manifest([case])
        for path in ("../secrets.go", "/tmp/x.go", "internal/../x.go", "internal/item/item_test.go"):
            with self.subTest(path=path), self.assertRaises(ValueError):
                validate_manifest([dict(case, path=path)])
        with self.assertRaises(ValueError):
            validate_manifest([case, case])
        with self.assertRaises(ValueError):
            validate_manifest([])


if __name__ == "__main__":
    unittest.main()
