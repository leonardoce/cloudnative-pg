# Database-Level Grants Specification

A specification of the database-level privileges that the `Database`
resource manages through `spec.permissions`: `CONNECT`, `CREATE` and
`TEMPORARY`, granted to or revoked from a role, including `PUBLIC`.
It started from the informal description in
[#10826](https://github.com/cloudnative-pg/cloudnative-pg/issues/10826).

The specification is **functional**, not behavioural. It defines which
`spec.permissions` are valid and which statements have to be executed for
a given `spec.permissions` and a given ACL of the database. It has no
`Init`, `Next` or `Spec`: it is meant to be read, and to guide and review
the implementation, and it is not model-checked. Its examples, written as
`ASSUME`s in a companion module, are evaluated by TLC (see "How to check").

User documentation: `docs/src/declarative_database_management.md`
("Managing Database Permissions").

## Files

* `DatabaseLevelGrants.tla`: the specification.
* `DatabaseLevelGrantsExamples.tla`, `DatabaseLevelGrantsExamples.cfg`: worked
  examples of the specification, written as `ASSUME`s, and an empty `.cfg`.
  TLC evaluates the `ASSUME`s when it starts and fails on the first one that
  does not hold, so they work as tests of the specification: the hardening
  case, `all`, the idempotence of a second pass, the owner filter, a role
  that does not exist, and which specifications are valid.

## How to check

From `tla/`:

```sh
make check-database-level-grants/DatabaseLevelGrantsExamples
```

This evaluates the examples with TLC. It parses the specification first, and it
also finds what parsing cannot, such as comparing an integer with a string.
Run it after every edit. `make check` runs it with the other models.

To try an expression, define it in the specification or in the examples
module and evaluate it in the editor, or add an `ASSUME PrintT(...)` to the
examples and run the command above. TLC has no interactive REPL in the version
pinned by the `Makefile`.

## What it defines

| Operator | Meaning | Implementation |
|---|---|---|
| `AreSpecifiedPermissionsFormallyCorrect` | Which `spec.permissions` are valid: a role appears once per privilege, and a role in `all` appears in no other list | `validatePermissions` in `internal/webhook/v1/database_webhook.go` |
| `ExpandPermissions` | `all` is a shortcut for the three privileges, expanded before reconciling | `expandDatabasePermissions` in `internal/management/controller/database_permissions.go` |
| `MapDatabaseRoleToOid`, `DecoratePermissionRecord` | Resolve each role to the OID that identifies it in the ACL: `0` for `public`, `notfound` if the role does not exist | `decorateDatabasePermissions` |
| `FilterACLItemSet` | Only the privileges granted by the database owner count, as it is the grantor PostgreSQL records when a superuser runs `GRANT` and `REVOKE` | the `a.grantor = d.datdba` condition of `getDatabaseACL` |
| `ApplyPermissionSet` | The statements needed to reach the requested state: a `GRANT` only if the privilege is missing, a `REVOKE` only if it is held, and an error for a role that does not exist | `planDatabasePermissions` and `reconcileDatabasePermissions` |

## Decisions the specification records

* `public`, exactly lowercase, is the `PUBLIC` pseudo-role, which
  PostgreSQL reserves as a role name. `PUBLIC` and `Public` are ordinary
  role names: PostgreSQL accepts them, so names are matched exactly.
* The current ACL is read as `aclexplode(coalesce(datacl,
  acldefault('d', datdba)))`. A NULL `datacl` stands for the default ACL,
  which includes the `CONNECT` and `TEMPORARY` that `PUBLIC` holds on a new
  database; reading the raw column would hide them.
* A role that does not exist is an error, for a `revoke` too, so that a
  mistake in the specification is noticed. The other entries are still
  applied.
* Repeating a role for a privilege is rejected, whether the entries are
  identical or conflicting. The check runs on the specified lists, before
  `all` is expanded, as the union of the expansion would merge identical
  entries and hide the repetition.

## Assumptions and limits

* Statements are strings in which the database name is the placeholder
  `<xx>` and the roles are not quoted. The implementation quotes both as
  identifiers, except for `PUBLIC`.
* The examples show the behaviour on a few inputs: they are not exhaustive.
* It does not model the status reported by the controller, the errors of
  executing a statement, the order of the statements, or the origin of an
  entry expanded from `all`.
* `WITH GRANT OPTION` and `GRANTED BY` are out of scope.
