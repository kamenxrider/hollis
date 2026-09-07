import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('host_matrix', Path(__file__).with_name('host_matrix.py'))
matrix = importlib.util.module_from_spec(spec)
spec.loader.exec_module(matrix)


class MatrixTests(unittest.TestCase):
    def test_pacing_journal_and_no_repeat(self):
        for model, delay in [('cloud', 10), ('cloud-pro', 45)]:
            with self.subTest(model=model), tempfile.TemporaryDirectory() as temp:
                root = Path(temp); binary = root / 'hollis'; binary.write_text('fixture')
                out = root / 'results'; out.mkdir()
                matrix.write_json(out / 'pacing.json', {'ended': 100, 'blocked': {}})
                manifest = {'output': str(out), 'binary': str(binary), 'binary_sha256': matrix.digest(binary),
                            'env': {}, 'kit': str(root), 'cases': [{'id':'one', 'sequence':model, 'model':model, 'arguments':['respond','fixture']}]}
                def invoke(command, **kwargs):
                    if command[-1] == 'path': return subprocess.CompletedProcess(command, 0, str(binary)+'\n','')
                    intent = json.loads((out/'one.json').read_text())
                    self.assertIsNone(intent['exit_code'])
                    return subprocess.CompletedProcess(command,0,'{"results":{"response":"fixture"}}','')
                with patch.object(matrix.time,'time',return_value=100), patch.object(matrix.time,'sleep') as sleep, patch.object(matrix.subprocess,'run',side_effect=invoke) as run, contextlib.redirect_stdout(io.StringIO()):
                    matrix.run_case(manifest,'one')
                    sleep.assert_called_once_with(delay)
                    self.assertEqual(run.call_count,2)  # one read-only resolution and one request
                    with self.assertRaisesRegex(RuntimeError,'receipt already'):matrix.run_case(manifest,'one')
                    self.assertEqual(run.call_count,2)

    def test_missing_image_is_recorded_and_blocks_sequence(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            binary = root / 'hollis'; binary.write_text('fixture')
            cases = [{'id': name, 'sequence': 'image', 'arguments': ['image', 'generate', 'fixture'],
                      'image_output': str(root / (name + '.png'))} for name in ['one', 'two']]
            manifest = {'output': str(root / 'out'), 'binary': str(binary), 'binary_sha256': matrix.digest(binary),
                        'env': {}, 'kit': str(root), 'cases': cases}
            replies = [subprocess.CompletedProcess([], 0, str(binary) + '\n', ''),
                       subprocess.CompletedProcess([], 0, '{"results":{}}', '')]
            with patch.object(matrix.subprocess, 'run', side_effect=replies) as run, patch.object(matrix.time, 'sleep'), contextlib.redirect_stdout(io.StringIO()):
                matrix.run_case(manifest, 'one')
                receipt = json.loads((root / 'out/one.json').read_text())
                self.assertEqual(receipt['outcome'], 'invalid_image')
                self.assertEqual(receipt['exit_code'], 0)
                with self.assertRaisesRegex(RuntimeError, 'Sequence stopped'):
                    matrix.run_case(manifest, 'two')
                self.assertEqual(run.call_count, 2)

    def test_receipt_created_during_lock_acquisition_prevents_dispatch(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp); output = root / 'out'; output.mkdir()
            original_mkdir = Path.mkdir
            def acquire(path, *args, **kwargs):
                original_mkdir(path, *args, **kwargs)
                if path.name == '.active':
                    (output / 'one.json').write_text('{"outcome":"success"}')
            manifest = {'output': str(output), 'cases': [{'id': 'one'}]}
            with patch.object(Path, 'mkdir', acquire), patch.object(matrix.subprocess, 'run') as run:
                with self.assertRaisesRegex(RuntimeError, 'receipt already'):
                    matrix.run_case(manifest, 'one')
                run.assert_not_called()

    def test_unknown_failure_blocks_sequence(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);binary=root/'hollis';binary.write_text('fixture')
            cases=[{'id':name,'sequence':'image','arguments':['image','generate','fixture']} for name in ['one','two']]
            m={'output':str(root/'out'),'binary':str(binary),'binary_sha256':matrix.digest(binary),'env':{},'kit':str(root),'cases':cases}
            replies=[subprocess.CompletedProcess([],0,str(binary)+'\n',''),subprocess.CompletedProcess([],5,'{"error":{"code":"shortcut_failed"}}','')]
            with patch.object(matrix.subprocess,'run',side_effect=replies) as run,patch.object(matrix.time,'sleep'),contextlib.redirect_stdout(io.StringIO()):
                matrix.run_case(m,'one')
                with self.assertRaisesRegex(RuntimeError,'Sequence stopped'):matrix.run_case(m,'two')
                self.assertEqual(run.call_count,2)


if __name__=='__main__':unittest.main()
