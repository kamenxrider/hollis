"""Model-free attachment decoding checks using generated images only."""
import json
import os
import struct
import subprocess
import tempfile
import unittest
import zlib
from pathlib import Path

import helper_protocol as protocol


class ImageInputTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not protocol.can_execute():
            raise unittest.SkipTest('image harness requires Apple silicon macOS 27')
        cls.temp = tempfile.TemporaryDirectory(prefix='hollis-image-input-')
        cls.addClassCleanup(cls.temp.cleanup)
        cls.folder = Path(cls.temp.name)
        cls.binary = cls.folder / 'image-harness'
        sdk = protocol.command('xcrun', '--sdk', 'macosx', '--show-sdk-path').stdout.strip()
        result = protocol.command(
            'xcrun', 'swiftc', '-sdk', sdk, '-target', 'arm64-apple-macosx27.0',
            str(protocol.ROOT / 'native/ImageInput.swift'),
            str(protocol.ROOT / 'scripts/native/testdata/image_harness/main.swift'),
            '-o', str(cls.binary))
        if result.returncode:
            raise AssertionError(result.stderr)

    def run_harness(self, *args):
        result = subprocess.run([str(self.binary), *map(str, args)],
                                capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 0)
        self.assertNotIn(str(self.folder).encode(), result.stdout + result.stderr)
        return json.loads(result.stdout)

    def make(self, name='image.png', media='png', width=80, height=40, orientation=1):
        path = self.folder / name
        self.assertEqual(self.run_harness('make', path, media, width, height, orientation),
                         {'ok': True})
        return path

    def test_png_and_jpeg_ignore_extensions(self):
        for media in ('png', 'jpeg'):
            path = self.make(media + '.dat', media)
            self.assertEqual(self.run_harness('load', path), {'width': 80, 'height': 40})

    def test_exif_orientation_applied(self):
        path = self.make('rotated.jpg', 'jpeg', orientation=6)
        self.assertEqual(self.run_harness('load', path), {'width': 40, 'height': 80})

    def test_downscale_longest_side(self):
        path = self.make('large.png', width=5000, height=100)
        result = self.run_harness('load', path)
        self.assertEqual(result['width'], 4096)
        self.assertLessEqual(result['height'], 100)

    def test_pixel_ceiling_before_decode(self):
        path = self.make('pixel-ceiling.png')
        data = bytearray(path.read_bytes())
        data[16:24] = struct.pack('>II', 8001, 8000)
        data[29:33] = struct.pack('>I', zlib.crc32(data[12:29]))
        path.write_bytes(data)
        self.assertEqual(self.run_harness('load', path), {'kind': 'invalid_image'})

    def test_symlink_fifo_directory_missing_and_relative(self):
        target = self.make('target.png')
        link = self.folder / 'link.png'
        link.symlink_to(target)
        fifo = self.folder / 'fifo.png'
        os.mkfifo(fifo)
        for path in (link, fifo, self.folder, self.folder / 'absent.png', 'relative.png'):
            self.assertEqual(self.run_harness('load', path), {'kind': 'invalid_image'})

    def test_file_ceiling(self):
        path = self.folder / 'oversized.png'
        with path.open('wb') as file:
            file.truncate((64 << 20) + 1)
        self.assertEqual(self.run_harness('load', path), {'kind': 'invalid_image'})

    def test_unsupported_content_and_damaged_images(self):
        for index, (data, kind) in enumerate((
            (b'not an image', 'unsupported_media'),
            (b'GIF89a', 'unsupported_media'),
            (b'\x89PNG\r\n\x1a\nbroken', 'invalid_image'),
            (b'\xff\xd8\xffbroken', 'invalid_image'),
        )):
            path = self.folder / f'bad-{index}.png'
            path.write_bytes(data)
            self.assertEqual(self.run_harness('load', path), {'kind': kind})

    def test_helper_rejects_damaged_image_without_generation(self):
        helper = protocol.configured_helper()
        if helper is None:
            helper = protocol.build_helper(self.folder)
        path = self.folder / 'broken.png'
        path.write_bytes(b'\x89PNG\r\n\x1a\nbroken')
        result = protocol.run(helper, stdin=protocol.request_bytes(
            3, 'complete', prompt='Synthetic decoder failure.',
            images=[{'path': str(path)}]))
        self.assertEqual(result.returncode, 0)
        self.assertNotIn(str(self.folder).encode(), result.stdout + result.stderr)
        events = protocol.events(result.stdout)
        self.assertEqual(len(events), 2)
        status, failure = events
        self.assertEqual(status['event'], 'status')
        self.assertEqual(failure['event'], 'error')
        self.assertEqual(failure['kind'], 'invalid_image' if status['capabilities']['images']['supported']
                         else 'unsupported_capability')


if __name__ == '__main__':
    unittest.main()
