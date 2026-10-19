# Security policy

## Supported versions

Security fixes are released for the **latest version of the current 1.13.x
line**. Older versions are not patched — please update to the latest release
(the hub can update itself and its hosts from "About").

## Reporting a vulnerability

Please report vulnerabilities **privately** through GitHub:
open the **Security** tab of this repository and press
**Report a vulnerability**. Only the maintainers see the report.

Please **do not** open a public issue, pull request or discussion about a
vulnerability, and do not publish details until a fix is released.

What helps us act quickly:

- nkt version and role: host agent, hub, nkt-edge (and which edge roles), web
  UI, mobile app or the n8n integration;
- the steps to reproduce, what you expected and what happened;
- the impact as you see it (what an attacker gains and what they need first —
  a viewer account, an API token, network access to a port);
- logs or a proof of concept, if you have them.

## What happens next

- We confirm that we received the report **within 7 days**.
- We assess it and tell you the plan (fix, workaround, or why we consider it
  not a vulnerability) **within 30 days**.
- The fix is released as a new version; after the release we publish a GitHub
  Security Advisory and credit you, unless you prefer not to be named.

## Scope

In scope: everything in this repository — `nkt` (host agent and hub),
`nkt-edge`, the web UI, the mobile app and the n8n integration.

Out of scope:

- third-party repositories and images used by the deployment examples in
  `examples/` (they are marked with `# WARNING`) — report those to their
  authors;
- vulnerabilities in dependencies that are already public — they are tracked
  by Dependabot and fixed with updates; report them here only if nkt uses the
  dependency in an exploitable way;
- findings that need an administrator account of the hub or host (an
  administrator can already run commands there by design), unless they let a
  viewer or an API token do more than allowed.

The security design and its limits are described in the guide:
[Security](https://piqab.github.io/nkt/en/guide/reference-security).

---

## Кратко по-русски

Об уязвимостях сообщайте **закрыто**: вкладка **Security** репозитория →
**Report a vulnerability** (сообщение видят только сопровождающие). Не
открывайте публичные issue и не публикуйте подробности до выхода исправления.

Исправления выходят для последней версии ветки 1.13.x. Получение сообщения
подтверждаем **в течение 7 дней**, оценку и план даём **в течение 30 дней**;
после выпуска исправления публикуется GitHub Security Advisory.

Сторонние репозитории и образы из примеров выкладки (`examples/`, помечены
`# WARNING`) — к их авторам. Устройство безопасности nkt описано в справке:
[Безопасность](https://piqab.github.io/nkt/guide/reference-security).
