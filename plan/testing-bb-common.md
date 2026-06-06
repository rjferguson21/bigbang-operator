# Testing bb-common rendering

To see the full set of resources bb-common can generate, render the chart
with the exhaustive values file checked into the bb-common repo:

```bash
helm template bb-common /home/rob/bb/bb-common/chart \
  -f /home/rob/bb/bb-common/full-api-values.yaml
```

This is the reference output the operator must reproduce when it renders
bb-common for a `Package` whose `spec` covers the same surface area.

Useful filters:

```bash
# Just one kind
helm template bb-common /home/rob/bb/bb-common/chart \
  -f /home/rob/bb/bb-common/full-api-values.yaml \
  | yq 'select(.kind == "NetworkPolicy")'

# One resource by name
helm template bb-common /home/rob/bb/bb-common/chart \
  -f /home/rob/bb/bb-common/full-api-values.yaml \
  | yq 'select(.metadata.name == "default-peer-auth")'
```
