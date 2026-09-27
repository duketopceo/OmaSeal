#!/usr/bin/env python3
"""unittest suite for contrib/omaseal-jev-audit (stdlib only — no pytest needed).

Run: python3 -m unittest contrib/test_omaseal_jev_audit.py -v
"""
import importlib.util, importlib.machinery, io, json, os, stat, tempfile, unittest
from unittest.mock import patch
from urllib.error import HTTPError

PATH = os.path.join(os.path.dirname(__file__), "omaseal-jev-audit")
loader = importlib.machinery.SourceFileLoader("omaseal_jev_audit", PATH)
spec = importlib.util.spec_from_loader("omaseal_jev_audit", loader)
mod = importlib.util.module_from_spec(spec)
loader.exec_module(mod)

FINDING_DEAD = {"kind": "dead_rule", "target": "gone/x", "detail": "rule matches no item"}
FINDING_UNCOVERED = {"kind": "uncovered", "target": "bank/checking", "detail": "no matching rule"}
FINDING_STALE = {"kind": "stale", "target": "old/svc", "detail": "not used in 120d"}


class TestGate(unittest.TestCase):
    def test_disabled_by_default(self):
        with tempfile.TemporaryDirectory() as d:
            with patch.object(mod, "STATE_FILE", os.path.join(d, "jev.json")):
                self.assertFalse(mod.enabled())

    def test_corrupt_state_is_disabled(self):
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "jev.json")
            open(p, "w").write("{broken")
            with patch.object(mod, "STATE_FILE", p):
                self.assertFalse(mod.enabled())

    def test_enabled_true_only(self):
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "jev.json")
            json.dump({"enabled": True}, open(p, "w"))
            with patch.object(mod, "STATE_FILE", p):
                self.assertTrue(mod.enabled())
            json.dump({"enabled": False}, open(p, "w"))
            with patch.object(mod, "STATE_FILE", p):
                self.assertFalse(mod.enabled())

    def test_main_exits_when_disabled(self):
        with tempfile.TemporaryDirectory() as d:
            with patch.object(mod, "STATE_FILE", os.path.join(d, "jev.json")), \
                 patch("sys.argv", ["omaseal-jev-audit"]), \
                 patch("sys.stderr", io.StringIO()) as err:
                self.assertEqual(mod.main(), 0)
                self.assertIn("disabled", err.getvalue())


class TestPayload(unittest.TestCase):
    def test_state_is_metadata_only(self):
        """The Jev request body must carry finding metadata only — assert no
        secret-ish field is ever present and the state is just kind/target/detail."""
        captured = {}

        def fake_urlopen(req, timeout=0):
            captured["body"] = json.loads(req.data.decode())
            return io.BytesIO(json.dumps({"answers": {"recommend": {"choice": "none"},
                                                      "confidence": {"noul": 0.1}}}).encode())

        state_in = "Finding kind: dead_rule\nTarget: gone/x\nDetail: rule matches no item"
        with patch.object(mod, "urlopen", fake_urlopen):
            mod.jev("KEY", state_in)

        body = captured["body"]
        # Payload shape is exactly model/state/questions — nothing else serializes.
        self.assertEqual(set(body), {"model", "state", "questions"})
        # State goes over verbatim: only the finding metadata passed in, nothing
        # pulled from the keyring or elsewhere.
        self.assertEqual(body["state"], state_in)
        self.assertNotIn("secret", state_in.lower())
        self.assertNotIn("value", state_in.lower())
        self.assertEqual(set(body["questions"]), {"recommend", "confidence"})


class TestChangeMapping(unittest.TestCase):
    def test_dead_rule_remove(self):
        ch = mod.change_for(FINDING_DEAD, "remove_rule", 0.9, {"gone/x": "ASK"})
        self.assertEqual(ch["action"], "remove")
        self.assertEqual(ch["pattern"], "gone/x")
        self.assertFalse(ch["expands_access"])
        self.assertTrue(ch["reduces_access"])  # removing an ASK rule removes a grant

    def test_remove_deny_expands(self):
        ch = mod.change_for(FINDING_DEAD, "remove_rule", 0.9, {"gone/x": "DENY"})
        self.assertTrue(ch["expands_access"])
        self.assertNotIn("reduces_access", ch)

    def test_uncovered_add_deny(self):
        ch = mod.change_for(FINDING_UNCOVERED, "add_deny", 0.8, {})
        self.assertEqual((ch["action"], ch["policy"], ch["pattern"]), ("add", "DENY", "bank/*"))
        self.assertFalse(ch["expands_access"])
        self.assertTrue(ch["reduces_access"])

    def test_uncovered_add_allow_expands(self):
        ch = mod.change_for(FINDING_UNCOVERED, "add_allow", 0.8, {})
        self.assertEqual(ch["policy"], "ALLOW")
        self.assertTrue(ch["expands_access"])

    def test_recommend_none_yields_no_change(self):
        self.assertIsNone(mod.change_for(FINDING_UNCOVERED, "none", 0.9, {}))
        self.assertIsNone(mod.change_for(FINDING_STALE, "none", 0.9, {}))

    def test_kind_action_mismatch_yields_no_change(self):
        # Jev can only remove dead rules; a remove_rule on an item finding is dropped.
        self.assertIsNone(mod.change_for(FINDING_UNCOVERED, "remove_rule", 0.9, {}))
        # add_* on a dead_rule (target is a pattern, not an item) is dropped.
        self.assertIsNone(mod.change_for(FINDING_DEAD, "add_deny", 0.9, {}))


class TestManifestParsing(unittest.TestCase):
    def test_policies_and_sha(self):
        with tempfile.NamedTemporaryFile("w", delete=False) as f:
            f.write("# comment\nALLOW  openrouter/default - key\nDENY   bank/*  - money\nASK    * - fallback\n")
            path = f.name
        try:
            policies = mod.manifest_policies(path)
            self.assertEqual(policies["openrouter/default"], "ALLOW")
            self.assertEqual(policies["bank/*"], "DENY")
            self.assertEqual(policies["*"], "ASK")
            self.assertEqual(len(mod.manifest_sha256(path)), 64)
        finally:
            os.unlink(path)


class TestItemPattern(unittest.TestCase):
    def test_service_star(self):
        self.assertEqual(mod.item_pattern("bank/checking"), "bank/*")
        self.assertIsNone(mod.item_pattern("has space/name"))
        self.assertIsNone(mod.item_pattern(""))


class TestMainFlow(unittest.TestCase):
    def _env(self, d, enabled=True):
        os.makedirs(os.path.join(d, ".config", "omaseal"))
        state = os.path.join(d, ".config", "omaseal", "jev.json")
        json.dump({"enabled": enabled}, open(state, "w"))
        manifest = os.path.join(d, "ai-manifest.txt")
        open(manifest, "w").write("ASK * - fallback\n")
        return state, manifest

    def _fake_jev(self, answers_by_target):
        """Deterministic mock: return the answer for the finding's target parsed
        from the state string — ThreadPoolExecutor scheduling order must not
        decide which answer a finding gets."""
        def fake(key, state):
            for target, answers in answers_by_target.items():
                if f"Target: {target}" in state:
                    return answers
            return {"error": "no mock answer"}
        return fake

    def test_writes_proposal_and_skips_errors(self):
        with tempfile.TemporaryDirectory() as d:
            state, manifest = self._env(d)
            audit = {"manifest": manifest, "rules": 1, "items": 3,
                     "findings": [FINDING_DEAD, FINDING_UNCOVERED]}
            answers = {"gone/x": {"recommend": {"choice": "remove_rule"}, "confidence": {"noul": 0.9}},
                       "bank/checking": {"error": "500: boom"}}

            with patch.object(mod, "STATE_FILE", state), \
                 patch.object(mod, "PROPOSAL_DIR", os.path.join(d, "prop")), \
                 patch.object(mod, "run_audit", lambda: audit), \
                 patch.object(mod, "get_key", lambda: "KEY"), \
                 patch.object(mod, "jev", self._fake_jev(answers)), \
                 patch("sys.argv", ["omaseal-jev-audit"]), \
                 patch("sys.stderr", io.StringIO()):
                self.assertEqual(mod.main(), 0)

            files = os.listdir(os.path.join(d, "prop"))
            self.assertEqual(len(files), 1)
            path = os.path.join(d, "prop", files[0])
            self.assertEqual(stat.S_IMODE(os.stat(path).st_mode), 0o600)
            p = json.load(open(path))
            self.assertEqual(p["generator"], "jev")
            self.assertEqual(len(p["manifest_sha256"]), 64)
            self.assertEqual(len(p["changes"]), 1)
            self.assertEqual(p["changes"][0]["action"], "remove")

            # R10: the run stamps last_run_at into jev.json, preserving other fields.
            st = json.load(open(state))
            self.assertTrue(st["enabled"])
            self.assertIn("last_run_at", st)

    def test_malformed_answers_skipped_no_traceback(self):
        """Non-numeric/non-finite confidence and non-dict answers must skip the
        finding silently — malformed network data is not a crash."""
        with tempfile.TemporaryDirectory() as d:
            state, manifest = self._env(d)
            audit = {"manifest": manifest, "rules": 1, "items": 4,
                     "findings": [FINDING_DEAD, FINDING_UNCOVERED, FINDING_STALE]}
            answers = {
                "gone/x": {"recommend": {"choice": "remove_rule"}, "confidence": {"noul": "high"}},
                "bank/checking": {"recommend": {"choice": "add_ask"}, "confidence": {"noul": float("nan")}},
                "old/svc": {"recommend": {"choice": "add_deny"}, "confidence": {"noul": 0.8}},
            }
            with patch.object(mod, "STATE_FILE", state), \
                 patch.object(mod, "PROPOSAL_DIR", os.path.join(d, "prop")), \
                 patch.object(mod, "run_audit", lambda: audit), \
                 patch.object(mod, "get_key", lambda: "KEY"), \
                 patch.object(mod, "jev", self._fake_jev(answers)), \
                 patch("sys.argv", ["omaseal-jev-audit"]), \
                 patch("sys.stderr", io.StringIO()):
                self.assertEqual(mod.main(), 0)
            p = json.load(open(os.path.join(d, "prop", os.listdir(os.path.join(d, "prop"))[0])))
            # only the well-formed old/svc answer produced a change
            self.assertEqual(len(p["changes"]), 1)
            self.assertEqual(p["changes"][0]["policy"], "DENY")

    def test_cli_rejects_bad_args_without_traceback(self):
        for argv in (["omaseal-jev-audit", "--limit", "abc"],
                     ["omaseal-jev-audit", "--limit", "-3"],
                     ["omaseal-jev-audit", "--out"],
                     ["omaseal-jev-audit", "--bogus"]):
            with tempfile.TemporaryDirectory() as d:
                state, _ = self._env(d)
                with patch.object(mod, "STATE_FILE", state), \
                     patch("sys.argv", argv), \
                     patch("sys.stderr", io.StringIO()):
                    self.assertRaises(SystemExit, mod.main)
                    try:
                        mod.main()
                    except SystemExit as e:
                        self.assertEqual(e.code, 2)


@unittest.skipUnless(os.environ.get("OMASEAL_JEV_IT"), "live check: set OMASEAL_JEV_IT=1")
class TestLive(unittest.TestCase):
    def test_real_call(self):
        key = mod.get_key()
        answers = mod.jev(key, "Finding kind: dead_rule\nTarget: test/x\nDetail: rule matches no item")
        self.assertIn("recommend", answers)
        self.assertIn("confidence", answers)


if __name__ == "__main__":
    unittest.main()
