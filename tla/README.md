# TLA+ Specifications

Formal models of CloudNativePG mechanisms, checked with TLC.
Layout: one folder per model holding `<Model>.tla` (PlusCal source plus
its translation), `<Model>.cfg`, and a `README.md` documenting that
model; the `Makefile` and `.gitignore` at this level apply to every
model. A functional specification, which has no behaviour to check, has
no `<Model>.cfg` of its own: its examples are written as `ASSUME`s in a
companion `<Model>Examples.tla`, with an empty `<Model>Examples.cfg`, and
TLC evaluates them.

## Models

* [`primary-lease`](primary-lease/) — safe primary election: what happens
  after a CloudNativePG primary loses its lease when fail-safe mode is
  active (non-cooperative old primary). The old primary is fenced by a
  lease-age monitor with explicit time bounds and a hard stop before
  the lease expires.
* [`database-level-grants`](database-level-grants/) — functional
  specification of the privileges that the `Database` resource grants and
  revokes on a database (`spec.permissions`): which configurations are
  valid and which `GRANT`/`REVOKE` statements are needed for a given ACL.
  Not model-checked: its worked examples are evaluated by TLC.

## Files

* `Makefile` — `check` / `translate` / `clean` targets covering every model.
* `.gitignore` — TLC output (`states/`) and translator backups (`*.old`).

## How to check

Preferred entry point, from `tla/` (ensures a Java runtime is present,
fetches `tla2tools.jar` from the pinned TLA+ release when missing,
re-translates PlusCal, then checks every model with
`-workers auto`):

```sh
make check
```

Changing a PlusCal algorithm requires re-translation before
re-checking. See the model folders for per-model details.

## Adding a new model

Add a folder holding `<Model>.tla` and `<Model>.cfg` (module name must
match the file name) plus a `README.md` documenting it. The `Makefile`
discovers it through its `.cfg` — no registration needed; link it under
`## Models` above.

A functional specification is added the same way. Its examples go in
`<Model>Examples.tla`, which extends the specification, with an empty
`<Model>Examples.cfg`: TLC evaluates the `ASSUME`s when it starts, so they
need no `Init` or `Next`. The `translate` step skips any module without a
PlusCal algorithm.
