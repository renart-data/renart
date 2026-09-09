# Team and hosted secret providers

Status: parked proposal; requires a separate product/trust decision. These
providers are not shipped features and do not block the local reliability work
in [secret-management.md](secret-management.md).

The existing provider-neutral binding/resolution contract is documented in
[backend](../architecture/backend.md). Extend it rather than adding another
credential store, connection translator, or browser DTO family.

## Team-provider slice

Select a concrete SOPS/age, Vault, or cloud-secret integration only after its
authentication, deployment, and live-test requirements are agreed. The existing
encrypted user-local vault is not this team provider.

Model provider profiles, supported operations, version pinning, rotation, and
lease expiry/renewal explicitly. Providers that cannot put/delete values must
advertise that restriction. Preserve whole-connection external-backend
compatibility without conflating it with per-field Renart bindings.

Resolve by project, environment, reference, purpose, and optional run identity.
Keep values short-lived and out of serialization; close/revoke leases at the
operation boundary. Test permission refusal, unavailable service, expiry,
rotation, cancellation, and concurrent environments using the same provider
contract as local storage.

## Hosted trust boundary

This requires a separately accepted hosted execution/authentication design.
Prefer a runner's workload identity and short-lived, narrowly scoped provider
credentials over storing long-lived customer cloud keys in a control plane.
Resolve only after admission; revoke or expire the grant after the run.

If a managed provider is selected, design authenticated envelope encryption:
per-tenant/project data keys wrapped by KMS, identity-bound encryption context,
and separate ciphertext/KMS access roles. An authenticated write-only endpoint
must never return the stored value. Queues, run specs, support tools, telemetry,
and links carry only safe metadata or opaque identifiers.

Separate metadata read, secret use, secret management, and provider-profile
administration. Management need not grant reveal. Audit actor, purpose, run,
version, and outcome without values; define retention and export policy.
Prove that no tenant/run grant can resolve another tenant's key.

## Prerequisites and non-goals

- Agree supported providers and the exact identity/lease model before coding.
- Revalidate provider-specific research against current primary documentation.
- Keep local/headless environment-reference workflows first-class.
- Do not claim protection from arbitrary local code explicitly given a secret
  or from a compromised process running as the same OS user.
- Do not advertise team/hosted providers in user docs before implementation,
  permission tests, operational evidence, and a reviewed support contract.

When selected, split local team-provider work from hosted infrastructure into
bounded plans. Until then this is a parked design, not a scheduled release task.
