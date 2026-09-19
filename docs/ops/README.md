# Operations

How polis.pub is run, by design: what an operator takes on by holding other people's keys, how the hosted
service and its background actors are built, and what a discovery-service operator can tune and check.

⚠️ **The source of the hosted service and of the discovery service is not in the public repository**, so
operational procedure — deployment, settings, alerts, runbooks, backup and recovery — is not documented here.
What is here is the design those procedures serve, which is the part a reviewer or another operator can use.

## Documentation

| Document | Audience | Description |
|----------|----------|-------------|
| [admin/operator-guide.md](admin/operator-guide.md) | Operators, reviewers | ⭐ **Start here.** The operator's manual: custody, the authority rule, the two channels, what a self-hoster gets, and tenant conduct |
| [general/concepts/actors.md](../general/concepts/actors.md) | Operators, reviewers | The eight background actors — what each checks or does, what it deliberately does not do, and the concepts above the roster |
| [admin/hosted-service.md](admin/hosted-service.md) | Operators, reviewers | The multi-tenant hosted service: architecture, tenant lifecycle, security notes, the wake endpoint, and the log format |
| [ds/admin/configuration.md](../ds/admin/configuration.md) | Operators | Discovery service: what an operator can tune, and the admin API's operator-policy model |
| [ds/admin/deployment.md](../ds/admin/deployment.md) | Operators | Discovery service: what a deployment consists of, and how to verify one |
