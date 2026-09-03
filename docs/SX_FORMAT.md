# `.sx` Format

Status: implementation spec

`.sx` is the canonical JSON serialization of a Structural Complexity Graph
defined by [STRUCTURAL_COMPLEXITY.md](STRUCTURAL_COMPLEXITY.md).

The initial schema version is:

```text
sx-v0
```

The initial structural complexity model version is:

```text
sc-v0
```

## Top-Level Shape

```json
{
  "metadata": {},
  "nodes": [],
  "edges": [],
  "roots": []
}
```

`nodes`, `edges`, and `roots` are sorted deterministically during canonical
encoding.

## Metadata

```json
{
  "schema_version": "sx-v0",
  "model_version": "sc-v0",
  "frontend_id": "go",
  "frontend_version": "go-v0",
  "translation_spec_version": "go-translation-v0",
  "source_unit_id": "relative/path.go"
}
```

`source_unit_id` identifies the compilation unit represented by the file. For a
single Go file it is the relative path.

## Source Mapping

Every node has a source mapping. Every edge has a source mapping unless it is
synthetic.

```json
{
  "path": "internal/foo/foo.go",
  "start_line": 12,
  "start_column": 3,
  "end_line": 14,
  "end_column": 4
}
```

Synthetic objects use the closest concrete source span:

```json
{
  "path": "internal/foo/foo.go",
  "start_line": 12,
  "start_column": 3,
  "end_line": 12,
  "end_column": 8,
  "synthetic": true,
  "synthetic_reason": "implicit_else_case"
}
```

## Nodes

```json
{
  "id": "node:example",
  "kind": "operation",
  "root_id": "root:pkg.Func",
  "source": {},
  "attributes": {}
}
```

Root nodes use kind `root` and include:

```json
{
  "root_kind": "function",
  "identity": "module/package.Func",
  "parameter_count": 2,
  "receiver_count": 0,
  "result_count": 1
}
```

State nodes include:

```json
{
  "owner_root_id": "root:pkg.Func",
  "name": "value"
}
```

Global and external state may omit `owner_root_id`.

## Edges

```json
{
  "id": "edge:example",
  "kind": "containment",
  "from": "root:pkg.Func",
  "to": "node:example",
  "source": {},
  "attributes": {}
}
```

Call edges include a resolution attribute:

```json
{
  "resolution": "resolved"
}
```

Allowed values are:

```text
resolved
external
unresolved
```

## Canonicalization

Canonical `.sx` JSON uses:

```text
two-space indentation
nodes sorted by id
edges sorted by id
roots sorted by id
```

Unknown fields are rejected by higher-level tooling only when a schema version
requires it. For `sx-v0`, the Go decoder ignores unknown JSON fields so newer
metadata can be carried through by external tools without affecting scoring.

## Validation

The validator implements the `SC-VAL-*` rules from
[STRUCTURAL_COMPLEXITY.md](STRUCTURAL_COMPLEXITY.md). Invalid `.sx` files must
not be scored.
