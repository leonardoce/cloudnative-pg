---- MODULE DatabaseLevelGrantsExamples ----
\* Worked examples of DatabaseLevelGrants, written as ASSUMEs: TLC evaluates
\* them when it starts and fails on the first one that does not hold. They
\* document the specification and guard it against regressions. They have no
\* behaviour to check, hence the empty .cfg.
EXTENDS DatabaseLevelGrants

\* The ACL of a database that was never customized: PUBLIC can connect and
\* create temporary tables, and the owner can do everything.
DefaultACL == {
    [grantor |-> databaseOwnerOid, grantee |-> 0, privilege_type |-> "CONNECT", is_grantable |-> FALSE],
    [grantor |-> databaseOwnerOid, grantee |-> 0, privilege_type |-> "TEMPORARY", is_grantable |-> FALSE],
    [grantor |-> databaseOwnerOid, grantee |-> databaseOwnerOid, privilege_type |-> "CONNECT", is_grantable |-> FALSE],
    [grantor |-> databaseOwnerOid, grantee |-> databaseOwnerOid, privilege_type |-> "CREATE", is_grantable |-> FALSE],
    [grantor |-> databaseOwnerOid, grantee |-> databaseOwnerOid, privilege_type |-> "TEMPORARY", is_grantable |-> FALSE]
}

\* "app" is not the owner of the database here: its OID is the one that
\* MapDatabaseRoleToOid returns for every role but "public".
AppOid == MapDatabaseRoleToOid("app")

Grant(name) == [name |-> name, type |-> "grant"]
Revoke(name) == [name |-> name, type |-> "revoke"]

Permissions(connect, create, temporary, all) ==
    [connect |-> connect, create |-> create, temporary |-> temporary, all |-> all]

Hardening == Permissions({Revoke("public"), Grant("app")}, {}, {}, {})

-----------------------------------

\* The headline use case: revoke CONNECT from PUBLIC, and grant it to a role.
ASSUME ApplyPermissionSet(Hardening, DefaultACL) =
    { "REVOKE CONNECT ON DATABASE <xx> FROM public",
      "GRANT CONNECT ON DATABASE <xx> TO app" }

\* Applying it again, once the ACL matches, generates nothing.
ASSUME LET afterwards ==
           { e \in DefaultACL : e.grantee /= 0 \/ e.privilege_type /= "CONNECT" }
           \cup {[grantor |-> databaseOwnerOid, grantee |-> AppOid,
                  privilege_type |-> "CONNECT", is_grantable |-> FALSE]}
       IN ApplyPermissionSet(Hardening, afterwards) = {}

\* "all" is a shortcut for the three privileges.
ASSUME ApplyPermissionSet(Permissions({}, {}, {}, {Grant("app")}), DefaultACL) =
    { "GRANT CONNECT ON DATABASE <xx> TO app",
      "GRANT CREATE ON DATABASE <xx> TO app",
      "GRANT TEMPORARY ON DATABASE <xx> TO app" }

\* Revoking something that is not held, or granting what is held, is a no-op.
ASSUME ApplyPermissionSet(Permissions({}, {Revoke("public")}, {}, {}), DefaultACL) = {}
ASSUME ApplyPermissionSet(Permissions({Grant("public")}, {}, {}, {}), DefaultACL) = {}

\* Only what the owner granted counts: a privilege that somebody else granted
\* to "app" does not make GRANT unnecessary, and REVOKE would not remove it.
ASSUME LET grantedByAnother ==
           {[grantor |-> 1, grantee |-> AppOid, privilege_type |-> "CONNECT", is_grantable |-> FALSE]}
       IN ApplyPermissionSet(Permissions({Grant("app")}, {}, {}, {}), grantedByAnother) =
          {"GRANT CONNECT ON DATABASE <xx> TO app"}

\* A role that does not exist is an error, for a revoke too.
ASSUME LET missing(type) ==
           [spec |-> [name |-> "ghost", type |-> type],
            privilege |-> "CONNECT", granteeOid |-> NotFoundOid]
       IN /\ ApplyPermissionSetOfPrivilege({missing("grant")}, DefaultACL) =
               {"-- ERROR: unknown role ghost"}
          /\ ApplyPermissionSetOfPrivilege({missing("revoke")}, DefaultACL) =
               {"-- ERROR: unknown role ghost"}

\* The same, with the other entries still applied.
ASSUME ApplyPermissionSetOfPrivilege(
           { [spec |-> Grant("ghost"), privilege |-> "CONNECT", granteeOid |-> NotFoundOid],
             [spec |-> Grant("app"), privilege |-> "CONNECT", granteeOid |-> AppOid] },
           DefaultACL) =
       { "-- ERROR: unknown role ghost", "GRANT CONNECT ON DATABASE <xx> TO app" }

-----------------------------------

\* Which specifications are valid.
ASSUME AreSpecifiedPermissionsFormallyCorrect(Hardening)
ASSUME AreSpecifiedPermissionsFormallyCorrect(Permissions({}, {}, {}, {Grant("app")}))

\* The same role, for the same privilege, both granted and revoked.
ASSUME ~AreSpecifiedPermissionsFormallyCorrect(
           Permissions({Grant("app"), Revoke("app")}, {}, {}, {}))

\* A role in "all" can't appear in another list, even with the same type...
ASSUME ~AreSpecifiedPermissionsFormallyCorrect(
           Permissions({Grant("app")}, {}, {}, {Grant("app")}))

\* ...nor with a different one.
ASSUME ~AreSpecifiedPermissionsFormallyCorrect(
           Permissions({Revoke("app")}, {}, {}, {Grant("app")}))

\* The same role may appear under different privileges.
ASSUME AreSpecifiedPermissionsFormallyCorrect(
           Permissions({Grant("app")}, {Grant("app")}, {Revoke("app")}, {}))

======================================
