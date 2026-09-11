# Plugin verification

The source distribution includes synthetic installer and packaging tests. They
exercise manifest consistency, checksums, executable permissions, safe extraction,
version compatibility, upgrade/rollback and preservation of user configuration.
They do not call models or contain recorded user conversations.

```sh
python3 -m unittest discover -s scripts/plugin -p 'test_*.py'
python3 scripts/plugin/package_plugin.py --validate-only
```

A published plugin must also verify the exact runtime assets against GitHub build
provenance. Source validation alone is not proof that a distributable archive
exists or that a fresh Mac has completed Apple's consent flow.

Live model runs, host transcripts, screenshots and test receipts are maintained
privately. They are not included in the plugin or downloadable source archives.
Use [setup status](setup.md) to distinguish installation, model availability and
an actual response on your own Mac.
