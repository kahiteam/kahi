# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest minor of 1.x | Yes |
| older minors of 1.x | Security fixes only |
| < 1.0 | No |

Only the latest minor release of each major version receives security updates.
Once a new minor is released, the previous minor is supported for 30 days
to allow time for upgrades.

## Reporting a Vulnerability

**Do not open a public issue for security vulnerabilities.**

### Primary Channel: GitHub Private Vulnerability Reporting

Use [GitHub private vulnerability reporting](https://github.com/kahiteam/kahi/security/advisories/new)
to submit a report directly through the repository. This keeps the report
confidential and allows us to collaborate on a fix before public disclosure.

### Email Fallback

If you cannot use GitHub private vulnerability reporting, send an email to:

```
schwicht+kahi+security@gmail.com
```

Include the following in your report:

- Description of the vulnerability
- Steps to reproduce (minimal reproduction case preferred)
- Affected version(s)
- Impact assessment (what an attacker could achieve)

## Response SLA

| Milestone | Timeframe |
|-----------|-----------|
| Acknowledgment | Within 48 hours |
| Initial assessment | Within 7 days |
| Fix or mitigation | Within 90 days |

If we cannot meet the 90-day fix timeline, we will communicate the delay
and provide a revised timeline before the deadline expires.

## Scope

The following are considered security issues:

- Remote code execution
- Privilege escalation (process escaping its configured uid/gid/umask)
- Unauthorized access to the control API or Unix socket
- Path traversal in configuration file inclusion
- Log injection leading to terminal escape attacks
- Denial of service through crafted configuration

The following are **not** considered security issues:

- Crashes or panics that require local access to trigger
- Performance degradation under heavy load
- Misconfiguration by the operator (e.g., world-readable socket)
- Vulnerabilities in dependencies (report these upstream; we will update)

## Coordinated Disclosure

We follow a coordinated disclosure process:

1. Reporter submits vulnerability through a private channel.
2. We acknowledge receipt within 48 hours.
3. We work with the reporter to understand and reproduce the issue.
4. We develop and test a fix on a private branch.
5. We assign a CVE identifier if applicable.
6. We release the fix and publish a security advisory simultaneously.
7. Reporter may publish their own writeup after the advisory is public.

We ask reporters to avoid public disclosure until a fix is available or
90 days have elapsed, whichever comes first.

## Security Configuration

This repository has the following GitHub security features enabled:

- **Private vulnerability reporting** -- confidential advisory workflow
- **Secret scanning** -- detects accidentally committed secrets
- **Push protection** -- blocks pushes containing detected secrets
