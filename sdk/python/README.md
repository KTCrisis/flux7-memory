# mem7 Python SDK

Python client for [mem7](https://github.com/KTCrisis/mem7) — governed memory substrate for AI agents.

```python
from mem7 import Mem7

m = Mem7("http://localhost:9070", token="my-token")
m.store("deploy.decision", "approved by ops lead", tags=["decision"], agent="supervisor")
results = m.search("deployment approval", limit=5)
```
