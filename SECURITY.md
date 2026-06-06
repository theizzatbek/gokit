# Security Policy

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security problems —
public issues are indexed by search engines and seen by attackers
before maintainers can ship a fix.

### Preferred channel

Use GitHub's [private security advisory][advisory] form on this
repository:

> https://github.com/theizzatbek/gokit/security/advisories/new

That gives you and the maintainer a private workspace for triage,
patch development, and coordinated disclosure. GitHub assigns a CVE
on request when the fix lands.

[advisory]: https://docs.github.com/en/code-security/security-advisories/working-with-repository-security-advisories

### Fallback

If you can't use GitHub advisories (no GitHub account, behind a
network that blocks GitHub, etc.), open a minimal public issue
saying only **"security report, please contact me privately"** with
a way for the maintainer to reach you (email, Matrix, Signal). We
will move to a private channel before any vulnerability details are
shared.

### What to include in your report

Even a brief report is useful — don't sit on a finding just because
you can't write a full PoC. Helpful items, in order of priority:

1. **What package / file / function** is affected.
2. **What an attacker can do** — execute code, read data, bypass
   auth, DoS, etc.
3. **Reproduction steps or PoC** — even a one-line `curl` or a few
   lines of Go calling the vulnerable function.
4. **Affected versions** if known. We'll figure it out from `git
   blame` otherwise.
5. **Any mitigation** the operator can apply before a patch ships.

You don't need to suggest a fix. Just describe the problem
accurately and we'll take it from there.

## Response timeline

The maintainer commits to:

- **Acknowledgement** within 7 days of report. (If you don't hear
  back, escalate via the public fallback above — your message may
  have hit a spam filter.)
- **First analysis** with severity classification within 14 days.
- **Patch availability** for `CRITICAL` / `HIGH` severity within 30
  days when reasonably possible. `MEDIUM` / `LOW` rolls into the
  next scheduled minor release.
- **Coordinated disclosure** after a fix is shipped, default
  embargo 90 days from initial report or 30 days from patch
  availability — whichever comes first. Negotiable with the
  reporter.

This is a single-maintainer best-effort project — these are
expectations, not contractual SLAs.

## Supported versions

Once `v1.0.0` ships, security fixes will be backported to:

- **Latest MINOR release**: always.
- **Previous MINOR release**: for 6 months after the next MINOR
  releases.
- **Older releases**: best-effort, no commitment.

For pre-`v1.0.0` releases, security fixes land in the next release
only — there is no backport policy during the `0.x` series.

| Version | Supported |
|---|---|
| `v1.x` latest | yes |
| `v1.x` previous minor | yes (6mo window) |
| `0.x` | no formal support — upgrade to v1 |

## Scope

In scope:

- Any package under `github.com/theizzatbek/gokit/...`.
- The `cmd/kit` and `cmd/fibermap` CLI binaries.
- The kit's example services in `examples/` ARE in scope for kit
  bugs that they expose; example-specific bugs (handler logic) are
  out.
- DDL files shipped with the kit (`*/schema.sql`).
- The bundled JSON Schema files under `schemas/`.

Out of scope (please report upstream):

- Bugs in transitively imported dependencies (`pgx`, `nats.go`,
  `fiber`, etc.). Report them to the dependency's own security
  channel; the kit will pick up the fix in a normal dependency
  update.
- Vulnerabilities in the Docker images the kit's testcontainers
  pulls (Postgres, NATS, Redis, etc.).
- Configuration mistakes by the operator that aren't caused by
  ambiguous kit defaults.

If you're not sure whether something is in scope, report it
anyway — false positives are cheap, missed reports are expensive.

## What you can expect

- **Acknowledgement** in any release notes / CHANGELOG entry for
  the fix, unless you ask to stay anonymous.
- **CVE assignment** when the vulnerability has user impact —
  GitHub's advisory flow handles the paperwork.
- **No bug bounty.** This is a community-maintained kit; there is
  no paid program. We deeply appreciate reports anyway.

## What we ask of you

- **Don't exploit the bug** beyond the minimum needed to demonstrate
  it. No customer data, no third-party systems, no opportunistic
  scans.
- **Don't share details publicly** until a fix is available, unless
  the embargo timeline elapses.
- **Don't social-engineer** maintainers or contributors.
