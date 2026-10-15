# bigbang-operator-package

Helm 4 library for a regular Package plus directly rendered hook NetworkPolicies.
See [the integration guide](../../docs/helm-hook-jobs.md) for values and lifecycle behavior.

```helm
{{ include "bigbang-operator.package" . }}
```

The bundled bb-common 1.6.1 chart was packaged without template modifications
from `big-bang/product/packages/bb-common` commit
`a50e21849d4f61bf6ec7f36e0f0295359a3e00c4`. Its own resource rendering is disabled;
this library calls its helpers with the consuming package's values. The archive
is included so consumers and CI do not need credentials for registry1 to render.
