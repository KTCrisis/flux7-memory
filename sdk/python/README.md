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
```
