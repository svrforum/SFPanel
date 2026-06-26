# Security Policy

SFPanel runs with **root privileges** and exposes a web terminal, container exec, firewall, package, file, and disk management surface. We take security reports seriously and appreciate responsible disclosure.

## Supported versions

Security fixes are provided for the **latest released version** only. Please upgrade before reporting, and verify the issue still reproduces on the current release.

| Version | Supported |
|---------|-----------|
| latest `v0.x` release | ✅ |
| older releases | ❌ (upgrade first) |

## Reporting a vulnerability

**Please do _not_ open a public GitHub issue for security vulnerabilities.**

Use one of these private channels instead:

1. **GitHub Private Vulnerability Reporting** (preferred) —
   <https://github.com/svrforum/SFPanel/security/advisories/new>
2. **Email** — `svrforum.com@gmail.com` with the subject prefixed `[SFPanel Security]`.

Please include:

- Affected version (`sfpanel version`) and OS / install method.
- A clear description and, where possible, a minimal proof-of-concept.
- Impact assessment (what an attacker can do) and any suggested fix.

## What to expect

- **Acknowledgement** within **5 business days**.
- An initial assessment and severity triage within **10 business days**.
- We aim to ship a fix and publish a coordinated advisory as quickly as the
  severity warrants. We will keep you updated and credit you in the advisory
  unless you prefer to remain anonymous.

## Coordinated disclosure

Please give us a reasonable window (typically up to **90 days**, sooner for
actively exploited issues) to release a fix before any public disclosure. We
will work with you on the timeline.

## Scope notes

- SFPanel is **single-admin** and is designed to sit **behind a reverse proxy
  with TLS, not exposed directly to the public Internet**. Reports that assume a
  raw `0.0.0.0` exposure are still welcome, but please note the intended
  deployment model.
- The release supply chain is signed with **cosign keyless** provenance; binary
  integrity / signature issues are in scope.

---

## 보안 정책 (한국어 요약)

SFPanel은 **root 권한**으로 동작하며 웹 터미널·컨테이너 exec·방화벽·파일·디스크 관리 기능을 제공합니다. 보안 취약점은 **공개 이슈로 올리지 마시고** 아래 비공개 채널로 제보해 주세요:

1. **GitHub 비공개 취약점 제보**(권장): <https://github.com/svrforum/SFPanel/security/advisories/new>
2. **이메일**: `svrforum.com@gmail.com` (제목에 `[SFPanel Security]` 표기)

최신 릴리즈에 대해서만 보안 패치를 제공합니다. 5영업일 내 접수 확인, 조율된 공개(coordinated disclosure)를 원칙으로 합니다.
