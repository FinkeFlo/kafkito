# kafkito

<div style="background:#fff;padding:12px 16px;border-radius:10px;display:inline-block;margin:8px 0 16px;">
  <img src="assets/branding/logo.svg" alt="kafkito logo" width="520" />
</div>

> Manage and observe Apache Kafka clusters from a single Go binary.

kafkito is an open-source web UI for Kafka clusters: topics, messages,
consumer groups, schemas, ACLs, and RBAC-aware operations in one place.

## Get started

- **UI documentation:** [Getting started in the UI](ui/getting-started-ui.md)
- **Quickstart:** see the [README](https://github.com/FinkeFlo/kafkito/blob/main/README.md)
- **HTTP API:** [API reference](API.md)
- **Design notes:** [ADR overview](adr/0001-greenfield-apache2.md)

## What this site will cover

- Product overview and screenshots
- Local setup and deployment
- API and operational workflows
- Architecture decisions and implementation notes

## Related docs

| Page | Purpose |
| --- | --- |
| [UI: Getting started](ui/getting-started-ui.md) | Navigation, cluster selection, and first steps in the UI |
| [UI: Add cluster](ui/add-cluster.md) | Add private clusters, test connections, and avoid common pitfalls |
| [UI: Features overview](ui/features-overview.md) | What each UI area can do (Fleet, Topics, Groups, Schemas, Security, Brokers, Settings) |
| [UI: Workflows](ui/workflows.md) | Practical flows like finding messages, checking lag, and understanding offsets |
| [API](API.md) | REST endpoints and examples |
| [ADR-0001](adr/0001-greenfield-apache2.md) | Project foundation |
| [ADR-0002](adr/0002-tech-stack.md) | Chosen stack |
| [ADR-0003](adr/0003-cloud-foundry-readiness.md) | Cloud Foundry readiness |
| [ADR-0004](adr/0004-xsuaa-build-tag.md) | XSUAA build-tag plugin |
