Specification of the database-level grants of the `Database` resource,
informally described in
https://github.com/cloudnative-pg/cloudnative-pg/issues/10826.

This is a functional specification, not a behavioural one. It defines which
`spec.permissions` are valid (AreSpecifiedPermissionsFormallyCorrect) and
which statements have to be executed for a given `spec.permissions` and a
given ACL of the database (ApplyPermissionSet). It is meant to be read, and
to guide the implementation: it has no Init, Next or Spec, and it is not
meant to be model-checked.

The `spec.permissions` stanza looks like this:

```
spec:
  permissions:
    connect:
    - name: public
      type: revoke
    - name: app
      type: grant
    create:
    - name: app
      type: grant
    temporary:
    - name: app
      type: revoke
```

This is how you can get the list of ACL items from PG of a database.

```
-- Each of these records have the following structure:
-- record( grantor oid, grantee oid, privilege_type text, is_grantable boolean )

postgres=# select aclexplode(coalesce(datacl, acldefault('d', datdba))) from pg_database where datname='template1';
    aclexplode
---------------------
(10,0,CONNECT,f)
(10,10,CREATE,f)
(10,10,TEMPORARY,f)
(10,10,CONNECT,f)
(4 rows)
```

Grantee is "0" for "PUBLIC".

You can get the role name using the pg_roles catalog table.

---- MODULE DatabaseLevelGrants ----
EXTENDS Sequences, Integers

--------------------------------------

\* This is the specification of the API changes and tells the
\* configurations that are actually formally correct.

GrantOrRevoke == {"grant", "revoke"}
PermissionRecord == [name:STRING, type:GrantOrRevoke]

\* This is modeled as a set, but in the API is it implemented as
\* a list. Therefore we need to have a CEL rule disallowing
\* duplicates. If this is too cumbersome to implement as a CEL
\* rule, a code-level check in the webhook will do the trick.
PermissionRecordSet == SUBSET PermissionRecord

SpecifiedPermissions == [
    connect:PermissionRecordSet,
    create:PermissionRecordSet,
    temporary:PermissionRecordSet,
    all:PermissionRecordSet
]

ExpandPermissions(p) == [
    connect |-> p.connect \cup p.all,
    create |-> p.create \cup p.all,
    temporary |-> p.temporary \cup p.all
]

\* Two distinct records of the same list can't refer to the same role,
\* which is a role being both granted and revoked a privilege.
IsPermissionRecordSetCoherent(s) ==
    \A x \in s, y \in s : x.name = y.name => x = y

\* No role is named by both sets, whatever the type of the records is.
AreNamesDisjoint(a, b) ==
    {r.name : r \in a} \cap {r.name : r \in b} = {}

\* This tells whether a permission set is correct or not.
\* It should be implement as a CEL rule or, if it is too cumbersome,
\* a code-level check in the webhook. At this moment, I believe the
\* webhook to be the best place for it.
\*
\* The check is done on the specified lists, before expanding "all": the
\* union done by ExpandPermissions merges identical records, which would hide
\* a role that is repeated, and a repeated role is most likely a mistake.
\* Therefore a role can appear only once in the specified lists of a privilege,
\* and, as "all" applies to every privilege, a role in "all" can't appear in any
\* other list. Note that this rejects also the case where the two records
\* are identical.
AreSpecifiedPermissionsFormallyCorrect(p) ==
    /\ IsPermissionRecordSetCoherent(p.connect)
    /\ IsPermissionRecordSetCoherent(p.create)
    /\ IsPermissionRecordSetCoherent(p.temporary)
    /\ IsPermissionRecordSetCoherent(p.all)
    /\ AreNamesDisjoint(p.connect, p.all)
    /\ AreNamesDisjoint(p.create, p.all)
    /\ AreNamesDisjoint(p.temporary, p.all)

-----------------------------------

\* The following definitions are simplified to contain only the
\* part that we intend to model.

ConnectPrivilegeType == "CONNECT"
CreatePrivilegeType == "CREATE"
TemporaryPrivilegeType == "TEMPORARY"

PrivilegeType == {ConnectPrivilegeType, CreatePrivilegeType, TemporaryPrivilegeType}
PGACLItem == [grantor:Int, grantee:Int, privilege_type:PrivilegeType, is_grantable:BOOLEAN]
PGACLItemSet == SUBSET PGACLItem

\* This is just a example, and the database owner OID can be get using the datdba column of pg_database
databaseOwnerOid == 19823

\* The OID of a role that does not exist. OIDs are unsigned, so it is never a real one.
\* It is an integer, and not a string such as "notfound", because TLC refuses to
\* compare, or to test the membership of, values of different types.
NotFoundOid == -1

GranteeOIDType == Int

IsKnownOid(oid) == oid /= NotFoundOid

\* This is just a example, and the role name can be get by using the following query - the only case
\* where this really matters is the "public" user:
\*
\*    select oid from pg_roles WHERE rolname = $1
\*
\* For the purpose of this specification, this function can return NotFoundOid too.
\* That happens when the query return no rows.
\*
\* Important - and document that :
\*
\* postgres=# CREATE ROLE "PUBLIC";
\*   CREATE ROLE
\*   postgres=# CREATE ROLE "Public";
\*   CREATE ROLE
MapDatabaseRoleToOid(roleName) == IF roleName="public" THEN 0 ELSE 29322

\* We only care about the permissions we granted, but: https://www.postgresql.org/docs/18/sql-grant.html
\*
\* If a superuser chooses to issue a GRANT or REVOKE command, the command is performed as though
\* it were issued by the owner of the affected object. In particular, privileges granted via such
\* a command will appear to have been granted by the object owner. (For role membership, the
\* membership appears to have been granted by the bootstrap superuser.)
\*
\* In the ACL item set, "ALL" is expanded as "CREATE", "TEMPORARY", and "CONNECT".
FilterACLItemSet(s) == {e \in s : e.grantor=databaseOwnerOid }

\* This specifies what does it mean to apply a set of permissions. The current set of ACLs can
\* be downloaded from PG by using:
\*
\*     select aclexplode(coalesce(datacl, acldefault('d', datdba)))
\*     from pg_database where datname='template1';
\*
\* Important: the grantor field will contain the OID of the owner of the database since
\* we're granting and revoking privileges using a superuser.
\* The FilterACLItemSet takes care of that.
\*
\* Important 2: in the generated queries, there's no escaping. It should be properly done when implemented.
\* Idem for the database name.

DecoratedPermissionRecord == [spec:PermissionRecord, privilege:PrivilegeType, granteeOid:GranteeOIDType]

MakeGrantStatement(privilege, grantee) == "GRANT " \o privilege \o " ON DATABASE <xx> TO " \o grantee

MakeRevokeStatement(privilege, grantee) == "REVOKE " \o privilege \o " ON DATABASE <xx> FROM " \o grantee

RoleHasPermission(acls, roleOid, privilege) ==
    \E x \in acls : /\ x.grantee=roleOid
                    /\ x.privilege_type=privilege

MarkUnknownRoles(p) ==
    {"-- ERROR: unknown role " \o s.spec.name : s \in p}

ApplyPermissionSetOfPrivilegeGrant(p, acls) ==
    LET neededChanges == {s \in p: ~RoleHasPermission(acls, s.granteeOid, s.privilege)}
    IN { MakeGrantStatement(s.privilege, s.spec.name) : s \in neededChanges }

ApplyPermissionSetOfPrivilegeRevoke(p, acls) ==
    LET neededChanges == {s \in p: RoleHasPermission(acls, s.granteeOid, s.privilege)}
    IN { MakeRevokeStatement(s.privilege, s.spec.name) : s \in neededChanges }

\* Important: this spec requires erroring out revokes to unexisting roles.
\* this is on-purpose and let's the user detect a mistake in the permission spec.
ApplyPermissionSetOfPrivilege(p, acls) ==
    MarkUnknownRoles({s \in p : ~IsKnownOid(s.granteeOid)}) \cup
    ApplyPermissionSetOfPrivilegeGrant({s \in p : s.spec.type="grant" /\ IsKnownOid(s.granteeOid)}, acls) \cup
    ApplyPermissionSetOfPrivilegeRevoke({s \in p : s.spec.type="revoke" /\ IsKnownOid(s.granteeOid)}, acls)

DecoratePermissionRecord(r, privilege) ==
    [spec |-> r, privilege |-> privilege, granteeOid |-> MapDatabaseRoleToOid(r.name)]

ApplyPermissionSet(spec, currentACL) ==
    LET expandedSpec == ExpandPermissions(spec)
        decoratedRecords ==
            {DecoratePermissionRecord(r, CreatePrivilegeType) : r \in expandedSpec.create} \cup
            {DecoratePermissionRecord(r, ConnectPrivilegeType) : r \in expandedSpec.connect} \cup
            {DecoratePermissionRecord(r, TemporaryPrivilegeType) : r \in expandedSpec.temporary}
        filteredACL == FilterACLItemSet(currentACL)
    IN
       ApplyPermissionSetOfPrivilege(decoratedRecords, filteredACL)

======================================