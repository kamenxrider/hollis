"""Protocol tests for the helper's `extract` operation against generated PDFs.

The helper is driven exactly as the Go runner drives it, with an absolute path
to a document the test created. Extraction uses PDFKit and Vision only: the
helper emits its status event and then the extraction result, and never opens a
session, so nothing here calls the on-device model. The status event's
availability fields depend on the host, so these cases assert the extract event
exactly and the status event by shape.
"""
import json
import os
import shutil
import tempfile
import unittest
from pathlib import Path

import helper_protocol as protocol

PHRASE = 'Banana bread recipe'


class ExtractProtocolTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.helper = protocol.configured_helper()
        if cls.helper is None and not protocol.can_execute():
            raise unittest.SkipTest(
                'the built helper requires Apple silicon macOS 27 to execute')
        cls.folder = Path(tempfile.mkdtemp(prefix='hollis-extract-protocol-'))
        cls.addClassCleanup(shutil.rmtree, cls.folder, True)
        if cls.helper is None:
            cls.helper = protocol.build_helper(cls.folder)
        cls.harness = protocol.build_extraction_harness(cls.folder)

    def fixture(self, mode, name, *args):
        path = self.folder / name
        answer = protocol.harness(self.harness, mode, str(path), *args)
        self.assertTrue(answer.get('ok'), answer)
        return path

    def extract(self, **fields):
        stdin = protocol.request_bytes(3, 'extract', **fields)
        process = protocol.run(self.helper, stdin=stdin, timeout=1200)
        self.assertEqual(process.returncode, 0, process.stderr)
        # PDFKit's own logging can print a fixed notice for a damaged file.
        # The runner never reads helper stderr; what must hold is that neither
        # stream carries the path or any document text.
        for stream in (process.stdout, process.stderr):
            self.assertNotIn(str(self.folder).encode(), stream)
        self.assertNotIn(b'Alpha', process.stderr)
        events = protocol.events(process.stdout)
        self.assertEqual(len(events), 2, process.stdout)
        status, result = events
        self.assertEqual(status['event'], 'status')
        self.assertEqual(status['capabilities']['extract'], {
            'pdf': True, 'ocr': True, 'pages': True,
            'max_pages': 2000, 'max_text_bytes': 131072})
        for event in events:
            self.assertEqual((event['protocol'], event['version'], event['model']),
                             (3, protocol.swift_pins()[0], 'local'))
        return {key: value for key, value in result.items()
                if key not in ('protocol', 'version', 'model')}

    def test_text_layer_complete_shape(self):
        path = self.fixture('make-text-pdf', 'text.pdf', '2', 'Alpha beta gamma.')
        self.assertEqual(self.extract(source={'path': str(path)}, media='pdf'), {
            'event': 'complete', 'media': 'pdf', 'page_count': 2, 'text_bytes': 48,
            'pages': [
                {'page': 1, 'text': 'Page 1\nAlpha beta gamma.', 'method': 'text'},
                {'page': 2, 'text': 'Page 2\nAlpha beta gamma.', 'method': 'text'},
            ]})

    def test_selection_reports_the_whole_page_count(self):
        path = self.fixture('make-text-pdf', 'select.pdf', '5', 'Selected.')
        result = self.extract(source={'path': str(path)}, pages=[2, 5])
        self.assertEqual(result['page_count'], 5)
        self.assertEqual([page['page'] for page in result['pages']], [2, 5])

    def test_scanned_page_is_marked_ocr(self):
        path = self.fixture('make-scan-pdf', 'scan.pdf', '1', PHRASE)
        result = self.extract(source={'path': str(path)})
        self.assertEqual(result['pages'][0]['method'], 'ocr')
        self.assertIn(PHRASE.lower(), result['pages'][0]['text'].lower())

    def test_page_out_of_range_reports_the_real_page_count(self):
        path = self.fixture('make-text-pdf', 'short.pdf', '2', 'Two pages.')
        self.assertEqual(self.extract(source={'path': str(path)}, pages=[1, 3]), {
            'event': 'error', 'kind': 'page_out_of_range', 'page': 3, 'page_count': 2})

    def test_blank_document_is_no_text(self):
        path = self.fixture('make-blank-pdf', 'blank.pdf')
        self.assertEqual(self.extract(source={'path': str(path)}),
                         {'event': 'error', 'kind': 'extract_no_text'})

    def test_page_ceiling_is_invalid_document_with_numbers(self):
        path = self.fixture('make-text-pdf', 'many.pdf', '2001', 'Ceiling.')
        self.assertEqual(self.extract(source={'path': str(path)}), {
            'event': 'error', 'kind': 'invalid_document', 'limit': 2000, 'actual': 2001})

    def test_text_ceiling_is_context_capacity_with_numbers(self):
        path = self.fixture('make-long-pdf', 'long.pdf', '45', '40', 'word ' * 16)
        result = self.extract(source={'path': str(path)})
        self.assertEqual(set(result), {'event', 'kind', 'limit', 'actual'})
        self.assertEqual((result['kind'], result['limit']), ('context_capacity', 131072))
        self.assertGreater(result['actual'], 131072)

    def test_non_pdf_content_is_unsupported_media(self):
        text = self.folder / 'notes.pdf'
        text.write_text('plain text with a pdf extension\n')
        self.assertEqual(self.extract(source={'path': str(text)}, media='pdf'),
                         {'event': 'error', 'kind': 'unsupported_media'})

    def test_unreadable_sources_are_invalid_document_without_a_path(self):
        missing = self.folder / 'absent.pdf'
        target = self.fixture('make-text-pdf', 'target.pdf', '1', 'Linked.')
        link = self.folder / 'link.pdf'
        link.symlink_to(target)
        damaged = self.folder / 'damaged.pdf'
        damaged.write_bytes(b'%PDF-1.7\nnot really a document\n')
        for path in (missing, link, damaged, self.folder):
            with self.subTest(path=path.name):
                self.assertEqual(self.extract(source={'path': str(path)}),
                                 {'event': 'error', 'kind': 'invalid_document'})


if __name__ == '__main__':
    unittest.main()
