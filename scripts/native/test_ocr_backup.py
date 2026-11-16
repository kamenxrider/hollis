"""Accurate-first OCR backup, using synthetic pages and no model routes."""
import os
from pathlib import Path
import shutil
import tempfile
import unittest

import helper_protocol as protocol

PHRASE = 'Banana bread recipe'
SWEEP = ['accurate'] + ['fast'] * 4
ORIENTATIONS = ['up', 'right', 'down', 'left']


class OCRBackupTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not protocol.can_execute():
            raise unittest.SkipTest('PDFKit and Vision extraction requires Apple silicon macOS 27')
        cls.folder = Path(tempfile.mkdtemp(prefix='hollis-ocr-backup-'))
        cls.addClassCleanup(shutil.rmtree, cls.folder, True)
        cls.harness = protocol.build_extraction_harness(cls.folder)

    def fixture(self, mode, name, *args):
        path = self.folder / name
        answer = protocol.harness(self.harness, mode, str(path), *args)
        self.assertTrue(answer.get('ok'), answer)
        return path

    def backup(self, path, fault):
        return protocol.harness(self.harness, 'extract-backup', str(path), fault)

    def test_accurate_throw_sweeps_fast_with_same_pixels_and_languages(self):
        for mode, name, args, words in (
                ('make-scan-pdf', 'scan.pdf', ('1', PHRASE), PHRASE),
                ('make-rotated-pdf', 'rotated.pdf', ('Rotated scan text',), 'rotated')):
            with self.subTest(mode=mode):
                path = self.fixture(mode, name, *args)
                answer = self.backup(path, 'accurate-throws')
                self.assertTrue(answer['ok'], answer)
                self.assertEqual(answer['attempts'], SWEEP)
                self.assertEqual(answer['orientations'], ORIENTATIONS)
                self.assertEqual(answer['render_calls'], 1)
                self.assertTrue(answer['same_image'])
                self.assertTrue(answer['same_languages'])
                self.assertEqual(answer['pages'][0]['method'], 'ocr')
                self.assertIn(words.lower(), answer['pages'][0]['text'].lower())

    def test_fast_backup_reads_each_quarter_turn(self):
        for rotation in (90, 180, 270):
            with self.subTest(rotation=rotation):
                path = self.fixture('make-rotated-pdf', f'turn-{rotation}.pdf',
                                    'Rotated scan text', str(rotation))
                answer = self.backup(path, 'accurate-throws')
                self.assertTrue(answer['ok'], answer)
                self.assertIn('rotated', answer['pages'][0]['text'].lower())
                self.assertEqual(answer['attempts'], SWEEP)
                self.assertEqual(answer['orientations'], ORIENTATIONS)
                self.assertEqual(answer['render_calls'], 1)
                self.assertTrue(answer['same_image'])
                self.assertTrue(answer['same_languages'])

    def test_both_levels_throw_is_invalid_document(self):
        path = self.fixture('make-scan-pdf', 'both.pdf', '1', PHRASE)
        answer = self.backup(path, 'both-throw')
        self.assertEqual(answer['failure'], 'invalid_document')
        self.assertEqual(answer['attempts'], SWEEP)
        self.assertEqual(answer['render_calls'], 1)

    def test_render_failure_never_attempts_recognition(self):
        path = self.fixture('make-scan-pdf', 'render.pdf', '1', PHRASE)
        answer = self.backup(path, 'render-nil')
        self.assertEqual(answer['failure'], 'invalid_document')
        self.assertEqual(answer['attempts'], [])
        self.assertEqual(answer['render_calls'], 1)

    def test_accurate_success_and_empty_results_never_use_fast(self):
        path = self.fixture('make-scan-pdf', 'success.pdf', '1', PHRASE)
        for fault in ('accurate-success', 'accurate-empty'):
            with self.subTest(fault=fault):
                answer = self.backup(path, fault)
                self.assertEqual(answer['attempts'], ['accurate'])
                self.assertEqual(answer['orientations'], [])
                self.assertEqual(answer['render_calls'], 1)
                if fault == 'accurate-success':
                    self.assertEqual(answer['pages'][0]['text'], PHRASE)
                else:
                    self.assertEqual(answer['failure'], 'extract_no_text')

    def test_cancellation_and_timeout_errors_never_use_fast(self):
        path = self.fixture('make-scan-pdf', 'cancel.pdf', '1', PHRASE)
        for fault in ('cancelled', 'vision-cancelled', 'vision-timeout',
                      'swift-cancelled', 'swift-timeout', 'url-cancelled',
                      'url-timeout', 'cocoa-cancelled', 'posix-cancelled',
                      'posix-timeout', 'underlying-timeout'):
            with self.subTest(fault=fault):
                answer = self.backup(path, fault)
                self.assertEqual(answer['failure'], 'invalid_document')
                self.assertEqual(answer['attempts'], ['accurate'])

    def test_fast_backup_that_finds_no_text_is_no_text(self):
        path = self.fixture('make-blank-pdf', 'blank.pdf')
        answer = self.backup(path, 'accurate-throws')
        self.assertEqual(answer['failure'], 'extract_no_text')
        self.assertEqual(answer['attempts'], SWEEP)

    def test_confidence_weights_characters_sums_lines_and_breaks_ties_stably(self):
        path = self.fixture('make-scan-pdf', 'selection.pdf', '1', PHRASE)
        for fault, expected in (('weighted', 'longer reading'),
                                ('summed', 'aa\nbb'), ('tie', 'prefer up')):
            with self.subTest(fault=fault):
                answer = self.backup(path, fault)
                self.assertTrue(answer['ok'], answer)
                self.assertEqual(answer['pages'][0]['text'], expected)
                self.assertEqual(answer['orientations'], ORIENTATIONS)

    def test_one_successful_orientation_survives_other_recognition_throws(self):
        path = self.fixture('make-scan-pdf', 'partial.pdf', '1', PHRASE)
        answer = self.backup(path, 'partial-throw')
        self.assertTrue(answer['ok'], answer)
        self.assertEqual(answer['pages'][0]['text'], 'last orientation')
        self.assertEqual(answer['attempts'], SWEEP)

    def test_cancellation_between_orientations_and_fast_errors_stop_sweep(self):
        path = self.fixture('make-scan-pdf', 'mid-cancel.pdf', '1', PHRASE)
        for fault in ('cancel-between', 'fast-cancelled', 'fast-timeout'):
            with self.subTest(fault=fault):
                answer = self.backup(path, fault)
                self.assertEqual(answer['failure'], 'invalid_document')
                self.assertEqual(answer['attempts'], ['accurate', 'fast'])
                self.assertEqual(answer['orientations'], ['up'])

    def test_short_upright_text_is_accepted_by_backup(self):
        path = self.fixture('make-scan-pdf', 'ok.pdf', '1', 'OK done')
        answer = self.backup(path, 'accurate-throws')
        self.assertTrue(answer['ok'], answer)
        self.assertIn('ok done', answer['pages'][0]['text'].lower())
        self.assertEqual(answer['orientations'], ORIENTATIONS)
        # Upright needs no minimum score: "OK" at 0.5 scores 1.0.
        answer = self.backup(path, 'upright-short')
        self.assertTrue(answer['ok'], answer)
        self.assertEqual(answer['pages'][0]['text'], 'OK')

    def test_upside_down_tie_keeps_upright_reading(self):
        # Documented limitation: fast scores a garbled upright reading of an
        # upside-down page the same as the correct one, and a tie keeps upright.
        path = self.fixture('make-scan-pdf', 'tie-down.pdf', '1', PHRASE)
        answer = self.backup(path, 'upside-down-tie')
        self.assertTrue(answer['ok'], answer)
        self.assertEqual(answer['pages'][0]['text'], 'MOU AEd')
        self.assertEqual(answer['orientations'], ORIENTATIONS)

    def test_sideways_reading_needs_a_clear_score(self):
        # Documented limitation: a turned page with only very short text
        # reads as no text when accurate recognition fails.
        path = self.fixture('make-scan-pdf', 'threshold.pdf', '1', PHRASE)
        for fault, expected in (('sideways-below', None), ('sideways-just-below', None),
                                ('up-throws-short', None), ('sideways-at', 'ab'),
                                ('sideways-not-above-up', 'abcd')):
            with self.subTest(fault=fault):
                answer = self.backup(path, fault)
                self.assertEqual(answer['orientations'], ORIENTATIONS)
                if expected is None:
                    self.assertEqual(answer['failure'], 'extract_no_text')
                else:
                    self.assertTrue(answer['ok'], answer)
                    self.assertEqual(answer['pages'][0]['text'], expected)

    def test_runner_deadline_and_cancellation_bound_real_slow_backup(self):
        path = self.fixture('make-scan-pdf', 'slow.pdf', '1', PHRASE)
        env = dict(os.environ, HOLLIS_TEST_OCR_HARNESS=str(self.harness),
                   HOLLIS_TEST_OCR_PDF=str(path))
        result = protocol.command(
            'go', 'test', './internal/runner',
            '-run', '^TestNativeExtractSlowOCRBackupBounded$', '-count=1', '-v',
            cwd=protocol.ROOT, env=env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
