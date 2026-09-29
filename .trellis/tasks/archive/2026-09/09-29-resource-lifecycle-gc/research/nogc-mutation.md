# Candidate-derived GC-disabled negative control

Main executed the independently authored public behavior test in a root-source snapshot. Only collectTerminalLocked was replaced with a no-op; all other source and tests were copied unchanged. This is distinct from the historical baseline directory named check-negative-nogc.

Command: `go test -count=1 -timeout=60s -run ^TestIndependentTerminalCatalogAndResolveReleased$ -v .`

Exit: 1

Candidate normalized reconcile SHA256: `076c4619519e3974719c8f9ebca70c876f549df35e893fdec8145ff3fb5ae90a`

Mutated normalized reconcile SHA256: `50b4ef61023c023d4d2d13e8c28aa8ac75384b4257ab753442024ec3b87e8ad5`

Independent test SHA256: `27f36d231a5f105e9caf214491cc0171204c5ad37b6c1d04ab7b27dd11725366`

See nogc-mutation.diff and nogc-mutation-red.txt for exact change and target assertion.
