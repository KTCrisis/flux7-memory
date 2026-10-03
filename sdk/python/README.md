# flux7-memory

Python client for [mem7](https://github.com/KTCrisis/flux7-memory) — governed memory substrate for AI agents.

```bash
pip install flux7-memory
```

```python
from mem7 import Mem7

m = Mem7("http://localhost:9070", token="my-token")
m.store("deploy.decision", "approved by ops lead", tags=["decision"], agent="supervisor")
results = m.search("deployment approval", limit=5)

# mem7 0.8: memories are bi-temporal
m.store("event7.host", "Cloudflare", valid_from="2026-03-20")
m.recall(key="event7.host", valid_at="2026-03-01")   # what held on March 1st
m.history("event7.host")                             # every version, author, trace, seal
```

`token=None` (the default) reads `MEM7_TOKEN` from the environment. Full API: [docs.flux7.art/mem7/python-sdk](https://docs.flux7.art/mem7/python-sdk/).
